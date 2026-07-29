// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

// extraMathName is the entry name under which the portable-constraint math file
// is registered in config.init.math_paths and applied via config.init.extra_math.
const extraMathName = "additional_math"

// emissionCostClass is the Calliope cost-class name used to carry co2 emissions
// (from carrier co2_intensity) so `cost[costs=co2]` totals emissions. It matches
// the emission-limit default and the constraint IR's emissions variable.
const emissionCostClass = "co2"

// calliopeSolver picks the solver name to emit. HiGHS cannot drive Calliope
// dev7 (a pyomo/persistent-solver incompatibility), so "highs" (and the unset
// default) map to cbc — a free, open-source solver that works with Calliope.
func calliopeSolver(name string) string {
	switch name {
	case "", "highs":
		return "cbc"
	default:
		return name
	}
}

// Emitter writes a Calliope v0.7 model: a model.yaml with `config`,
// `techs`, and `nodes`, CSV data tables for series-valued parameters, and JSON
// sidecars for constraints / non-constant performance (applied by the
// orchestrator via custom math).
type Emitter struct{}

// Emit writes model.yaml, its data tables, and the extra math file under
// outDir and returns the model.yaml path as the entrypoint.
func (Emitter) Emit(j *model.Job, outDir string) (string, error) {
	m := &j.Model
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}

	root := newYAML()

	// config ------------------------------------------------------------------
	cfg := newYAML()
	initN := newYAML()
	if len(m.Time.Subset) == 2 {
		// Calliope 0.7 nests the time subset under init.subset.timesteps
		// (a bare init.time_subset is rejected).
		ss := newYAML()
		ss.set("timesteps", []any{m.Time.Subset[0], m.Time.Subset[1]})
		initN.set("subset", ss)
	}
	// Run mode lives under config.init.mode in Calliope 0.7, and uses Calliope's
	// own names (base/operate/spores) — not the canonical ones. Translate here.
	mode := j.Experiment.EffectiveMode()
	initN.set("mode", calliopeMode(mode))
	// Scalar values assigned to timeseries-shaped parameters (source_use_equals,
	// flow_cap in operate mode, native passthrough values, ...) must be broadcast
	// over the missing dimensions; Calliope 0.7 rejects them otherwise.
	initN.set("broadcast_input_data", true)
	// Temporal aggregation: Calliope 0.7 supports plain resampling natively
	// (built-in clustering was removed; ValidateFor rejects other methods).
	if ta := j.Experiment.TimeAggregation; ta != nil && ta.Method == "resample" {
		rs := newYAML()
		rs.set("timesteps", strings.ToLower(ta.Resolution))
		initN.set("resample", rs)
	}
	cfg.set("init", initN)

	solve := newYAML()
	solve.set("solver", calliopeSolver(j.Experiment.Solver.Name))
	if so := calliopeSolverOptions(j.Experiment.Solver); so != nil {
		solve.set("solver_options", so)
	}
	if r := j.Experiment.Reporting; r != nil {
		if r.SaveLogs != "" {
			solve.set("save_logs", r.SaveLogs)
		}
		if len(r.ShadowPrices) > 0 {
			sp := make([]any, len(r.ShadowPrices))
			for i, s := range r.ShadowPrices {
				sp[i] = s
			}
			solve.set("shadow_prices", sp)
		}
	}
	// SPORES (canonical "alternatives") needs a config.solve.spores block to be
	// launchable. Defaults < legacy experiment.solver.options.spores <
	// first-class experiment.alternatives.
	if mode == model.ModeAlternatives {
		solve.set("spores", calliopeSpores(&j.Experiment))
	}
	cfg.set("solve", solve)

	// config.build carries the operate window/horizon and the unmet-demand
	// feasibility switch. Operate defaults < legacy
	// experiment.solver.options.operate < first-class experiment.operate.
	build := newYAML()
	if mode == model.ModeOperate {
		op := newYAML()
		op.set("window", "12h")
		op.set("horizon", "24h")
		if raw, ok := j.Experiment.Solver.Options["operate"]; ok {
			if over, ok := raw.(map[string]any); ok {
				for _, k := range emit.SortedKeys(over) {
					op.set(k, over[k])
				}
			}
		}
		if o := j.Experiment.Operate; o != nil {
			if o.Window != "" {
				op.set("window", strings.ToLower(o.Window))
			}
			if o.Horizon != "" {
				op.set("horizon", strings.ToLower(o.Horizon))
			}
		}
		build.set("operate", op)
	}
	if ud := j.Experiment.AllowUnmetDemand; ud != nil && ud.Enabled {
		// Calliope's slack is priced at bigM, not a user penalty; ValidateJob
		// warns about the approximation when penalty_price is set.
		build.set("ensure_feasibility", true)
	}
	if build.len() > 0 {
		cfg.set("build", build)
	}
	root.set("config", cfg)

	// techs -------------------------------------------------------------------
	var csvs []calliopeCSV // series-backed data tables to materialize
	anyCO2 := false        // whether any tech carries a co2 emission cost
	ms := &calliopeMathState{}
	dataTables := newYAML()
	techs := newYAML()
	for _, id := range emit.Keys(m.Technologies) {
		t := m.Technologies[id]
		node := t.Node.First()
		tn, err := calliopeTech(t.At(node), m, id, dataTables, &csvs, &anyCO2, ms)
		if err != nil {
			return "", err
		}
		if mode == model.ModeOperate {
			applyOperateCaps(tn, t.At(node))
		}
		techs.set(id, tn)
	}
	for _, id := range emit.Keys(m.Transmission) {
		techs.set(id, calliopeTransmission(m.Transmission[id], &anyCO2))
	}

	// trade -> market techs. Import: a supply tech selling the carrier at the
	// import price (co2 cost class carries the import emission factor). Export: a
	// flexible demand tech capped at the export limit and PAID the export price
	// (negative cost_flow_in = revenue).
	tradeTechs := map[string][]string{} // node -> generated tech names
	tradeIDs := make([]string, 0, len(m.Trade))
	for id := range m.Trade {
		tradeIDs = append(tradeIDs, id)
	}
	sort.Strings(tradeIDs)
	for _, id := range tradeIDs {
		tr := m.Trade[id]
		if tr.Import != nil {
			imp := newYAML()
			imp.set("base_tech", "supply")
			imp.set("carrier_out", tr.Carrier)
			if f, ok := tr.Import.Limit.Reduce(); ok {
				imp.set("flow_cap_max", f)
			}
			// Price and emission factor share one costs-indexed parameter:
			// monetary carries the price, the co2 class the emission factor.
			classes, values := []any{}, []any{}
			if f, ok := tr.Import.Price.Reduce(); ok && f != 0 {
				classes = append(classes, model.PrimaryCostClass)
				values = append(values, f)
			}
			if ef := tr.Import.EmissionFactor; ef != nil && *ef != 0 {
				classes = append(classes, emissionCostClass)
				values = append(values, *ef)
				anyCO2 = true
			}
			if len(classes) > 0 {
				cost := newYAML()
				cost.set("data", values)
				cost.set("index", classes)
				cost.set("dims", "costs")
				imp.set("cost_flow_out", cost)
			}
			if err := mergeNativeIntoYAML(imp, tr.Native.For(model.TargetCalliope)); err != nil {
				return "", fmt.Errorf("trade %q: %w", id, err)
			}
			techs.set(id+"_import", imp)
			tradeTechs[tr.Node] = append(tradeTechs[tr.Node], id+"_import")
		}
		if tr.Export != nil {
			exp := newYAML()
			exp.set("base_tech", "demand")
			exp.set("carrier_in", tr.Carrier)
			if f, ok := tr.Export.Limit.Reduce(); ok {
				exp.set("sink_use_max", f)
			}
			// Price and emission factor share one costs-indexed parameter, like
			// the import side: monetary carries the revenue (negative cost),
			// the co2 class the emissions attributed per MWh exported.
			classes, values := []any{}, []any{}
			if f, ok := tr.Export.Price.Reduce(); ok && f != 0 {
				classes = append(classes, model.PrimaryCostClass)
				values = append(values, -f)
			}
			if ef := tr.Export.EmissionFactor; ef != nil && *ef != 0 {
				classes = append(classes, emissionCostClass)
				values = append(values, *ef)
				anyCO2 = true
			}
			if len(classes) > 0 {
				cost := newYAML()
				cost.set("data", values)
				cost.set("index", classes)
				cost.set("dims", "costs")
				exp.set("cost_flow_in", cost)
			}
			if err := mergeNativeIntoYAML(exp, tr.Native.For(model.TargetCalliope)); err != nil {
				return "", fmt.Errorf("trade %q: %w", id, err)
			}
			techs.set(id+"_export", exp)
			tradeTechs[tr.Node] = append(tradeTechs[tr.Node], id+"_export")
		}
	}
	root.set("techs", techs)

	// nodes -------------------------------------------------------------------
	nodes := newYAML()
	for _, id := range emit.Keys(m.Nodes) {
		n := m.Nodes[id]
		nn := newYAML()
		if n.Coords != nil {
			// Force float rendering so Calliope sees matching lat/lon types even
			// when a coordinate is a whole number (49.0, not int-like 49).
			nn.set("latitude", yamlFloat(n.Coords.Lat))
			nn.set("longitude", yamlFloat(n.Coords.Lon))
		}
		if n.AvailableArea != nil {
			nn.set("available_area", *n.AvailableArea)
		}
		tset := newYAML()
		for _, tid := range emit.Keys(m.Technologies) {
			if t := m.Technologies[tid]; model.ContainsStr(t.Node, id) {
				tset.set(tid, calliopeNodeOverride(t, id))
			}
		}
		for _, tid := range tradeTechs[id] {
			tset.set(tid, nil)
		}
		// Calliope requires every node to declare techs, even as an empty
		// mapping (techs: {}) — e.g. a hub only crossed by transmission links.
		nn.set("techs", tset)
		if err := mergeNativeIntoYAML(nn, n.Native.For(model.TargetCalliope)); err != nil {
			return "", fmt.Errorf("node %q: %w", id, err)
		}
		nodes.set(id, nn)
	}
	root.set("nodes", nodes)

	// When co2 is carried as a cost class, keep it out of the objective (it is
	// tracked for emission limits, not minimized like money): weight it 0.
	// Other cost classes keep their default weight of 1. The SPORES cost slack
	// and timestep weights are also data definitions, not solve keys.
	dd := newYAML()
	if anyCO2 {
		ocw := newYAML()
		ocw.set("data", 0)
		ocw.set("index", emissionCostClass)
		ocw.set("dims", "costs")
		dd.set("objective_cost_weights", ocw)
	}
	if a := j.Experiment.Alternatives; a != nil && a.Slack != nil && mode == model.ModeAlternatives {
		dd.set("spores_slack", *a.Slack)
	}
	if w := m.Time.Weights; w != nil {
		if f, ok := w.Reduce(); ok {
			dd.set("timestep_weights", f)
		}
	}
	if dd.len() > 0 {
		root.set("data_definitions", dd)
	}

	if dataTables.len() > 0 {
		root.set("data_tables", dataTables)
	}

	// Extra math assembly. Portable constraints + emission limits render into
	// the math file when custom math is enabled (default); ratio-pinning for
	// multi-port flows and piecewise performance are physics and render
	// unconditionally. Committable techs additionally request the built-in milp
	// math. In Calliope 0.7 a custom math file is registered by name under
	// config.init.math_paths, then applied via config.init.extra_math (order:
	// base -> mode -> extra) — it is NOT a config.build key.
	useMath := j.Experiment.CustomMathEnabled()
	havePortable := len(m.Constraints) > 0 || len(m.EmissionLimits) > 0
	renderPortable := havePortable && useMath
	needMathFile := renderPortable || len(ms.flowTechs) > 0 || len(ms.pwTechs) > 0
	var extraMath []any
	if ms.anyMILP {
		extraMath = append(extraMath, "milp")
	}
	if needMathFile {
		paths := newYAML()
		paths.set(extraMathName, "additional_math.yaml")
		initN.set("math_paths", paths)
		extraMath = append(extraMath, extraMathName)
	}
	if len(extraMath) > 0 {
		initN.set("extra_math", extraMath)
	}

	path := filepath.Join(outDir, "model.yaml")
	if err := os.WriteFile(path, []byte(yamlDoc(root)), 0o644); err != nil {
		return "", err
	}

	// Materialize the series-backed data tables referenced above, so the model
	// is directly runnable (Calliope reads these CSVs at load time).
	for _, c := range csvs {
		if err := writeCalliopeCSV(outDir, c, m); err != nil {
			return "", err
		}
	}

	if needMathFile {
		doc := calliopeMathDoc(m, ms, renderPortable)
		if err := os.WriteFile(filepath.Join(outDir, "additional_math.yaml"), []byte(yamlDoc(doc)), 0o644); err != nil {
			return "", err
		}
	}
	if havePortable && !useMath {
		if err := writeJSONSidecar(outDir, "_constraints.json", map[string]any{
			"constraints":     m.Constraints,
			"emission_limits": m.EmissionLimits,
			"note":            "custom_math disabled; register a math file in config.init.math_paths and apply it via config.init.extra_math",
		}); err != nil {
			return "", err
		}
	}
	return path, nil
}

// calliopeMode translates a canonical run mode into Calliope 0.7's own
// config.init.mode name. Calliope calls the cost-optimal run "base" (not
// "plan") and near-optimal alternatives "spores" (not "alternatives"). Other
// canonical modes never reach this emitter (the capability matrix rejects them
// for Calliope), so they pass through unchanged as a defensive default.
func calliopeMode(m model.Mode) string {
	switch m {
	case model.ModePlan:
		return "base"
	case model.ModeAlternatives:
		return "spores"
	case model.ModeOperate:
		return "operate"
	default:
		return string(m)
	}
}

// calliopeSpores builds the config.solve.spores block for a SPORES run. It
// defaults to a single extra iteration count that makes the run launchable;
// the legacy experiment.solver.options.spores namespace can extend/override
// any key (e.g. tracking_parameter), and the first-class
// experiment.alternatives fields win last. The slack is a data definition
// (spores_slack), not a solve key — Emit places it there.
func calliopeSpores(e *model.Experiment) *yamlNode {
	sp := newYAML()
	sp.set("number", 3) // default: 3 alternatives after the base run

	if raw, ok := e.Solver.Options["spores"]; ok {
		if over, ok := raw.(map[string]any); ok {
			ks := make([]string, 0, len(over))
			for k := range over {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			for _, k := range ks {
				sp.set(k, over[k])
			}
		}
	}
	if a := e.Alternatives; a != nil {
		if a.Number > 0 {
			sp.set("number", a.Number)
		}
		if a.ScoringAlgorithm != "" {
			sp.set("scoring_algorithm", a.ScoringAlgorithm)
		}
	}
	return sp
}

// calliopeSolverOptions maps the canonical solver fields (time_limit seconds,
// mip_gap, threads) onto config.solve.solver_options using the emitted
// solver's own option names. Unmapped solvers get none (ValidateJob warns).
func calliopeSolverOptions(s model.Solver) *yamlNode {
	names := map[string][3]string{
		"gurobi": {"TimeLimit", "MIPGap", "Threads"},
		"cbc":    {"seconds", "ratioGap", "threads"},
	}
	keys, ok := names[calliopeSolver(s.Name)]
	if !ok {
		return nil
	}
	so := newYAML()
	if s.TimeLimit != nil {
		so.set(keys[0], *s.TimeLimit)
	}
	if s.MIPGap != nil {
		so.set(keys[1], *s.MIPGap)
	}
	if s.Threads != nil {
		so.set(keys[2], *s.Threads)
	}
	if so.len() == 0 {
		return nil
	}
	return so
}

// applyOperateCaps supplies the capacity input parameters operate mode needs:
// Calliope 0.7 deactivates the capacity decision variables (flow_cap,
// storage_cap) in operate mode, so fixed capacities must arrive as input
// parameters — otherwise the solve finishes but postprocessing aborts on the
// missing flow_cap result. Storage must also opt out of cyclic behavior.
func applyOperateCaps(n *yamlNode, t model.Technology) {
	if t.Role == model.RoleDemand {
		return
	}
	if v, ok := fixedCap(t.Capacity); ok {
		n.set("flow_cap", v)
	}
	if t.Role == model.RoleStorage {
		n.set("cyclic_storage", false)
		if t.Storage != nil {
			if v, ok := fixedCap(t.Storage.EnergyCapacity); ok {
				n.set("storage_cap", v)
			}
		}
	}
}

// fixedCap resolves the fixed capacity of a tech for operate mode: the existing
// base if set, else the upper bound.
func fixedCap(c *model.Capacity) (float64, bool) {
	if c == nil {
		return 0, false
	}
	if c.Existing > 0 {
		return c.Existing, true
	}
	if c.Max != nil {
		return *c.Max, true
	}
	return 0, false
}

func calliopeBaseTech(r model.Role) string {
	switch r {
	case model.RoleSupply:
		return "supply"
	case model.RoleDemand:
		return "demand"
	case model.RoleConversion:
		return "conversion"
	case model.RoleStorage:
		return "storage"
	}
	return "supply"
}

// calliopeMathState collects, across the tech loop, everything the extra math
// file must render: multi-port flow techs (ratio pinning), piecewise techs
// (piecewise_constraints + balance_conversion masking), and whether the MILP
// math must be requested.
type calliopeMathState struct {
	flowTechs []calliopeFlowTech
	pwTechs   []string
	anyMILP   bool
}

// calliopeFlowTech is one multi-port conversion the math file pins: every
// non-reference flow is fixed to ratio * the reference input flow.
type calliopeFlowTech struct {
	id  string
	ref string // reference input carrier
}

func calliopeTech(t model.Technology, m *model.Model, id string, dataTables *yamlNode, csvs *[]calliopeCSV, anyCO2 *bool, ms *calliopeMathState) (*yamlNode, error) {
	n := newYAML()
	n.set("base_tech", calliopeBaseTech(t.Role))

	switch t.Role {
	case model.RoleSupply:
		n.set("carrier_out", t.CarrierOut.First())
		if f, ok := emit.PerfEfficiency(t).Reduce(); ok {
			n.set("flow_out_eff", f)
		}
		// Availability (operation.equals_pu/max_pu, or a physics precomputed
		// profile) becomes the source model per unit of flow_cap: a fixed
		// dispatch pins source_use_equals, a ceiling pins source_use_max. An
		// explicit technology.source block wins over the availability route.
		if av, param := calliopeAvailability(t); av != nil {
			if av.IsSeries() {
				addDataTable(dataTables, csvs, id, t.Node.First(), param, av.SeriesID())
				n.set("source_unit", "per_cap")
			} else if f, ok := av.Reduce(); ok {
				n.set(param, f)
				n.set("source_unit", "per_cap")
			}
		}
		if s := t.Source; s != nil {
			if s.Max != nil {
				if s.Max.IsSeries() {
					addDataTable(dataTables, csvs, id, t.Node.First(), "source_use_max", s.Max.SeriesID())
				} else if f, ok := s.Max.Reduce(); ok {
					n.set("source_use_max", f)
				}
			}
			if s.Unit != "" {
				n.set("source_unit", s.Unit)
			}
			if s.Cap != nil {
				n.set("source_cap_max", *s.Cap)
			}
		}
	case model.RoleDemand:
		n.set("carrier_in", t.CarrierIn.First())
		// A curtailable demand serves at most the profile (sink_use_max)
		// instead of exactly (sink_use_equals).
		sinkParam := "sink_use_equals"
		if t.DemandCurtailable {
			sinkParam = "sink_use_max"
		}
		if v, ok := t.DemandProfile.Reduce(); ok {
			// A scalar demand still becomes a timesteps-indexed data table:
			// Calliope 0.7 requires at least one timeseries data input to build
			// its time index, and an all-scalar model would otherwise abort with
			// "Must define at least one timeseries data input".
			addScalarDataTable(dataTables, csvs, id, t.Node.First(), sinkParam, v, m)
		} else if t.DemandProfile.IsSeries() {
			addDataTable(dataTables, csvs, id, t.Node.First(), sinkParam, t.DemandProfile.SeriesID())
		}
	case model.RoleConversion:
		if len(t.Flows) > 0 {
			if ref, needsMath := calliopeFlows(n, t.Flows); needsMath {
				ms.flowTechs = append(ms.flowTechs, calliopeFlowTech{id: id, ref: ref})
			}
		} else {
			n.set("carrier_in", t.CarrierIn.First())
			n.set("carrier_out", t.CarrierOut.First())
			if f, ok := emit.PerfEfficiency(t).Reduce(); ok {
				n.set("flow_out_eff", f)
			}
		}
		// Piecewise part-load performance: constant flow_in/flow_out breakpoint
		// vectors derived from the fixed capacity (ValidateFor guarantees one),
		// consumed by the math file's piecewise_constraints entry.
		if t.Performance.Kind() == model.PerfPiecewise {
			capacity := t.Capacity.Existing
			xs, ys, idx := []any{}, []any{}, []any{}
			for i, b := range t.Performance.Breakpoints {
				out := b.Load * capacity
				ys = append(ys, out)
				xs = append(xs, out/b.Efficiency)
				idx = append(idx, i)
			}
			px := newYAML()
			px.set("data", xs)
			px.set("index", idx)
			px.set("dims", "breakpoints")
			n.set("piecewise_x", px)
			py := newYAML()
			py.set("data", ys)
			py.set("index", idx)
			py.set("dims", "breakpoints")
			n.set("piecewise_y", py)
			if t.Performance.MinLoad != nil {
				n.set("flow_out_min_relative", *t.Performance.MinLoad)
			}
			ms.pwTechs = append(ms.pwTechs, id)
		}
	case model.RoleStorage:
		car := t.CarrierOut.First()
		if car == "" {
			car = t.CarrierIn.First()
		}
		n.set("carrier_in", car)
		n.set("carrier_out", car)
		if t.Storage != nil {
			if t.Storage.DischargeEff != nil {
				n.set("flow_out_eff", *t.Storage.DischargeEff)
			}
			if t.Storage.ChargeEff != nil {
				n.set("flow_in_eff", *t.Storage.ChargeEff)
			}
			if t.Storage.SelfDischarge != nil {
				n.set("storage_loss", *t.Storage.SelfDischarge)
			}
			if t.Storage.Cyclic {
				n.set("cyclic_storage", true)
			}
			if t.Storage.InitialSOC != nil {
				n.set("storage_initial", *t.Storage.InitialSOC)
			}
			if t.Storage.MaxHours != nil && t.Capacity != nil && t.Capacity.Max != nil {
				n.set("storage_cap_max", *t.Capacity.Max**t.Storage.MaxHours)
			}
			if ec := t.Storage.EnergyCapacity; ec != nil {
				if ec.Max != nil {
					n.set("storage_cap_max", *ec.Max)
				}
				if ec.Min != nil {
					n.set("storage_cap_min", *ec.Min)
				}
			}
			if t.Storage.DepthOfDischarge != nil {
				n.set("storage_discharge_depth", *t.Storage.DepthOfDischarge)
			}
			if t.Storage.NoSimultaneousChargeDischarge {
				n.set("force_async_flow", true)
			}
			// Independent power/energy sizing: Calliope has one flow_cap for
			// both directions, so ValidateJob guarantees the two rates agree
			// when both are set.
			if r := storageRate(t.Storage); r != nil {
				n.set("flow_cap_per_storage_cap_max", *r)
			}
		}
	}

	// Unit commitment, Calliope-style: integer purchased units of a fixed size,
	// integer dispatch, and a min stable load. Calliope rejects
	// flow_cap_per_unit combined with flow_cap_max/min, so the continuous
	// capacity bounds are skipped — the cap is purchased_units_max * per-unit
	// size. ValidateFor already rejected the unsupported UC fields (start/stop
	// costs, up/down times) and guaranteed a resolvable unit size.
	if t.Operation == nil || !t.Operation.Committable {
		calliopeCapacity(n, t.Capacity)
	} else {
		unit, _ := unitSize(t.Capacity)
		n.set("cap_method", "integer")
		n.set("integer_dispatch", true)
		n.set("flow_cap_per_unit", unit)
		maxUnits := 1.0
		if t.Capacity != nil && t.Capacity.Max != nil && unit > 0 {
			maxUnits = math.Ceil(*t.Capacity.Max/unit - 1e-9)
		}
		if t.Capacity != nil && t.Capacity.UnitsMax != nil {
			maxUnits = float64(*t.Capacity.UnitsMax)
		}
		n.set("purchased_units_max", int(maxUnits))
		if t.Capacity != nil && t.Capacity.UnitsMin != nil {
			n.set("purchased_units_min", *t.Capacity.UnitsMin)
		}
		if c, ok := t.Costs[model.PrimaryCostClass]; ok && c.Purchase != nil {
			n.set("cost_purchase", indexedCost(model.PrimaryCostClass, *c.Purchase))
		}
		ms.anyMILP = true
	}
	if t.Lifetime != nil {
		n.set("lifetime", *t.Lifetime)
	}
	if t.Area != nil && t.Area.Max != nil {
		n.set("area_use_max", *t.Area.Max)
	}
	if !t.IsActive() {
		n.set("active", false)
	}
	calliopeCosts(n, t, m)
	calliopeOperation(n, t.Operation)
	if calliopeFlowCosts(n, t, m) {
		*anyCO2 = true
	}
	// Calliope-specific params ride in native.calliope, merged last so they can
	// override or extend the emitted attributes.
	if err := mergeNativeIntoYAML(n, t.Native.For(model.TargetCalliope)); err != nil {
		return nil, fmt.Errorf("tech %q: %w", id, err)
	}
	return n, nil
}

// calliopeOperation emits the shared operational limits Calliope supports
// directly (availability floor and ramping). Unit commitment (committable,
// start/shut costs, min up/down time) and time-varying availability (max_pu)
// are Calliope-MILP/source-model concerns — supply those via native.calliope.
func calliopeOperation(n *yamlNode, op *model.Operation) {
	if op == nil {
		return
	}
	if v, ok := op.MinPU.Reduce(); ok {
		n.set("flow_out_min_relative", v)
	}
	// Calliope's flow_ramping is a single symmetric per-timestep fraction.
	if f, ok := op.RampUp.Reduce(); ok {
		n.set("flow_ramping", f)
	} else if f, ok := op.RampDown.Reduce(); ok {
		n.set("flow_ramping", f)
	}
}

// calliopeFlowCosts builds the merged per-flow cost parameters. cost_flow_out
// carries each class's variable O&M plus the co2 class for a tech-level
// emission factor; cost_flow_in carries each class's fuel cost plus the co2
// class for the consumed carrier's co2 intensity (mirroring PyPSA's carrier
// co2_emissions on the fuel-consumption basis). Both are indexed by costs
// only — Calliope sums cost_flow_*|flow_* over carriers, so a costs-only rate
// charges the tech's whole flow; exact for single-fuel techs (the common
// case). Returns true if any co2 cost was added.
func calliopeFlowCosts(n *yamlNode, t model.Technology, m *model.Model) bool {
	anyCO2 := false

	outClasses, outValues := []any{}, []any{}
	inClasses, inValues := []any{}, []any{}
	for _, class := range emit.Keys(t.Costs) {
		c := t.Costs[class]
		if v, ok := c.VariableOM.Reduce(); ok {
			outClasses, outValues = append(outClasses, class), append(outValues, v)
		}
		if v, ok := c.FuelCost.Reduce(); ok {
			inClasses, inValues = append(inClasses, class), append(inValues, v)
		}
	}
	if t.EmissionFactor != nil && *t.EmissionFactor != 0 {
		outClasses, outValues = append(outClasses, emissionCostClass), append(outValues, *t.EmissionFactor)
		anyCO2 = true
	}

	// The co2 intensity of the (fuel) carrier this tech consumes.
	seen := map[string]bool{}
	intensity, found := 0.0, false
	consume := func(c string) {
		if c == "" || seen[c] || found {
			return
		}
		seen[c] = true
		if cr, ok := m.Carriers[c]; ok && cr.CO2Intensity != nil && *cr.CO2Intensity != 0 {
			intensity, found = *cr.CO2Intensity, true
		}
	}
	for _, c := range t.CarrierIn {
		consume(c)
	}
	for _, f := range t.Flows {
		if f.Direction == model.FlowIn {
			consume(f.Carrier)
		}
	}
	if found {
		inClasses, inValues = append(inClasses, emissionCostClass), append(inValues, intensity)
		anyCO2 = true
	}

	if len(outClasses) > 0 {
		n.set("cost_flow_out", indexedCostMulti(outClasses, outValues))
	}
	if len(inClasses) > 0 {
		n.set("cost_flow_in", indexedCostMulti(inClasses, inValues))
	}
	return anyCO2
}

// indexedCostMulti builds a costs-indexed parameter, keeping the scalar
// index/data form for a single entry (matching indexedCost's output).
func indexedCostMulti(classes, values []any) *yamlNode {
	n := newYAML()
	if len(classes) == 1 {
		n.set("data", values[0])
		n.set("index", classes[0])
	} else {
		n.set("data", values)
		n.set("index", classes)
	}
	n.set("dims", "costs")
	return n
}

// calliopeFlows renders a multi-port conversion's carrier lists. A simple
// one-in/one-out pair is expressed through flow_out_eff (Calliope's built-in
// balance is exact there). Anything wider needs ratio-pinning math: Calliope's
// balance_conversion only balances carrier SUMS, so indexed flow_out_eff would
// both leave the split flexible and double-count the fuel. In that case all
// efficiencies stay 1 and the returned reference carrier feeds the math file,
// which pins every flow to ratio * the reference input.
func calliopeFlows(n *yamlNode, flows []model.Flow) (ref string, needsMath bool) {
	var inC, outC []any
	for _, f := range flows {
		if f.Direction == model.FlowIn {
			if f.Reference || ref == "" {
				ref = f.Carrier
			}
			inC = append(inC, f.Carrier)
		} else {
			outC = append(outC, f.Carrier)
		}
	}
	if len(inC) == 1 {
		n.set("carrier_in", inC[0])
	} else {
		n.set("carrier_in", inC)
	}
	if len(outC) == 1 {
		n.set("carrier_out", outC[0])
	} else {
		n.set("carrier_out", outC)
	}
	if len(inC) == 1 && len(outC) == 1 {
		for _, f := range flows {
			if f.Direction == model.FlowOut {
				n.set("flow_out_eff", f.Ratio)
			}
		}
		return ref, false
	}
	return ref, true
}

// calliopeAvailability picks the per-timestep availability value of a supply
// tech and the source parameter it pins: operation.equals_pu (fixed dispatch,
// source_use_equals), else operation.max_pu (ceiling, source_use_max), else a
// physics model's precomputed profile.
func calliopeAvailability(t model.Technology) (*model.Value, string) {
	if t.Operation != nil && t.Operation.EqualsPU != nil {
		return t.Operation.EqualsPU, "source_use_equals"
	}
	if t.Operation != nil && t.Operation.MaxPU != nil {
		return t.Operation.MaxPU, "source_use_max"
	}
	if t.Performance != nil && t.Performance.Kind() == model.PerfPhysics {
		return t.Performance.Precomputed, "source_use_max"
	}
	return nil, ""
}

func calliopeCapacity(n *yamlNode, c *model.Capacity) {
	if c == nil {
		return
	}
	if c.Expandable {
		if max := c.EffectiveMax(); max != nil {
			n.set("flow_cap_max", *max)
		}
		if min := c.EffectiveMin(); min != nil {
			n.set("flow_cap_min", *min)
		}
	} else if c.Existing > 0 {
		// Existing capacity: "impossible" (default) fixes the size;
		// "continuous" lets the optimizer retire any part of it.
		n.set("flow_cap_max", c.Existing)
		if c.DecommissionMode() != "continuous" {
			n.set("flow_cap_min", c.Existing)
		}
	}
	if c.SystemwideMax != nil {
		n.set("flow_cap_max_systemwide", *c.SystemwideMax)
	}
	if c.SystemwideMin != nil {
		n.set("flow_cap_min_systemwide", *c.SystemwideMin)
	}
}

// calliopeCosts emits indexed cost parameters. Overnight investment passes
// through unchanged; Calliope annualizes internally via cost_interest_rate and
// lifetime (so we do NOT pre-annualize here). The global discount rate wins
// over the component interest rate. Per-flow costs (variable O&M, fuel) are
// merged separately in calliopeFlowCosts.
func calliopeCosts(n *yamlNode, t model.Technology, m *model.Model) {
	for _, class := range emit.Keys(t.Costs) {
		c := t.Costs[class]
		if c.InvestmentPerCapacity != nil {
			n.set("cost_flow_cap", indexedCost(class, *c.InvestmentPerCapacity))
			if rate := m.EffectiveRate(t); t.CostBasis != model.CostAnnualized && rate != nil {
				n.set("cost_interest_rate", indexedCost(class, *rate))
			}
		}
		if c.FixedOM != nil {
			n.set("cost_om_annual", indexedCost(class, *c.FixedOM))
		}
		if c.FixedOMFraction != nil {
			n.set("cost_om_annual_investment_fraction", indexedCost(class, *c.FixedOMFraction))
		}
		if c.InvestmentPerEnergyCapacity != nil {
			n.set("cost_storage_cap", indexedCost(class, *c.InvestmentPerEnergyCapacity))
		}
		if c.Purchase != nil {
			n.set("cost_purchase", indexedCost(class, *c.Purchase))
		}
	}
}

func indexedCost(class string, value float64) *yamlNode {
	n := newYAML()
	n.set("data", value)
	n.set("index", class)
	n.set("dims", "costs")
	return n
}

func calliopeTransmission(l model.Transmission, anyCO2 *bool) *yamlNode {
	n := newYAML()
	n.set("base_tech", "transmission")
	n.set("carrier_in", l.Carrier)
	n.set("carrier_out", l.Carrier)
	n.set("link_from", l.From)
	n.set("link_to", l.To)
	// Calliope transmission is bidirectional by default; mirror the canonical
	// flag (PyPSA gets the inverse treatment: p_min_pu=-1 when bidirectional).
	if !l.Bidirectional {
		n.set("one_way", true)
	}
	if f, ok := l.Efficiency.Reduce(); ok {
		n.set("flow_out_eff", f)
	}
	if l.Capacity != nil && l.Capacity.Max != nil {
		n.set("flow_cap_max", *l.Capacity.Max)
	}
	if l.Distance != nil {
		n.set("distance", *l.Distance)
	}
	// Per-distance loss: Calliope composes multiplicatively
	// (eff_per_distance^distance); 1 - loss matches the canonical linear loss
	// exactly at distance 1 and is the standard small-loss approximation
	// beyond (it never over-delivers).
	if l.LossPerDistance != nil {
		n.set("flow_out_eff_per_distance", 1-*l.LossPerDistance)
	}
	if l.MinFlow != nil {
		n.set("flow_out_min_relative", *l.MinFlow)
	}
	for _, class := range emit.Keys(l.Costs) {
		if c := l.Costs[class]; c.InvestmentPerCapacityDistance != nil {
			n.set("cost_flow_cap_per_distance", indexedCost(class, *c.InvestmentPerCapacityDistance))
		}
	}
	// Emissions per MWh transported ride the co2 cost class on the link flow.
	if l.EmissionFactor != nil && *l.EmissionFactor != 0 {
		n.set("cost_flow_out", indexedCost(emissionCostClass, *l.EmissionFactor))
		*anyCO2 = true
	}
	if !l.IsActive() {
		n.set("active", false)
	}
	return n
}

// storageRate resolves the single flow_cap-per-storage_cap bound Calliope
// supports from the canonical charge/discharge rates (equal when both set;
// enforced by ValidateJob).
func storageRate(s *model.Storage) *float64 {
	if s.MaxChargeRate != nil {
		return s.MaxChargeRate
	}
	return s.MaxDischargeRate
}

// calliopeNodeOverride returns a per-node override mapping (or nil for "enabled,
// no override"). Only capacity and cost overrides are projected.
func calliopeNodeOverride(t model.Technology, node string) *yamlNode {
	ov, ok := t.NodeOverrides[node]
	if !ok {
		return nil
	}
	n := newYAML()
	calliopeCapacity(n, ov.Capacity)
	for _, class := range emit.Keys(ov.Costs) {
		if c := ov.Costs[class]; c.InvestmentPerCapacity != nil {
			n.set("cost_flow_cap", indexedCost(class, *c.InvestmentPerCapacity))
		}
	}
	if n.len() == 0 {
		return nil
	}
	return n
}

// calliopeCSV is a series-backed data table the emitter must materialize: a
// timesteps-indexed CSV with the tech/node named in its two header rows. values
// (set for scalar expansion) takes precedence over the series reference.
type calliopeCSV struct {
	file, tech, node, series string
	values                   []float64
}

// addDataTable registers a CSV-backed parameter (e.g. a demand profile) in the
// data_tables block and records the CSV to materialize. The file is a
// timesteps-indexed table with `columns: [techs, nodes]` — a shape Calliope
// reads directly (the tech and node are named in the first two header rows).
func addDataTable(dt *yamlNode, csvs *[]calliopeCSV, tech, node, param, seriesID string) {
	entry := newYAML()
	entry.set("data", seriesID+".csv")
	entry.set("rows", "timesteps")
	entry.set("columns", []any{"techs", "nodes"})
	add := newYAML()
	add.set("parameters", param)
	entry.set("add_dims", add)
	dt.set(tech+"__"+param, entry)
	*csvs = append(*csvs, calliopeCSV{file: seriesID + ".csv", tech: tech, node: node, series: seriesID})
}

// addScalarDataTable registers a data table for a scalar-valued timeseries
// parameter by expanding the scalar over the model's snapshot index.
func addScalarDataTable(dt *yamlNode, csvs *[]calliopeCSV, tech, node, param string, v float64, m *model.Model) {
	file := tech + "_" + param + ".csv"
	entry := newYAML()
	entry.set("data", file)
	entry.set("rows", "timesteps")
	entry.set("columns", []any{"techs", "nodes"})
	add := newYAML()
	add.set("parameters", param)
	entry.set("add_dims", add)
	dt.set(tech+"__"+param, entry)

	vals := make([]float64, emit.SnapshotCount(m))
	for i := range vals {
		vals[i] = v
	}
	*csvs = append(*csvs, calliopeCSV{file: file, tech: tech, node: node, values: vals})
}

// writeCalliopeCSV materializes one series-backed data table. The two leading
// rows name the techs/nodes column dimensions (single column each); the
// remaining rows are `timestep,value` pairs indexed by the snapshot calendar.
func writeCalliopeCSV(dir string, c calliopeCSV, m *model.Model) error {
	vals := c.values
	if vals == nil {
		var ok bool
		vals, ok = emit.SeriesValues(m, c.series)
		if !ok {
			return fmt.Errorf("calliope data table %s: %q is not an inline time series", c.file, c.series)
		}
	}
	labels := emit.SnapshotLabels(m.Time, len(vals))
	rows := [][]string{{"techs", c.tech}, {"nodes", c.node}}
	for i, label := range labels {
		rows = append(rows, []string{label, emit.Ftoa(emit.SeriesAt(vals, i))})
	}
	return emit.WriteCSV(dir, c.file, rows)
}

func writeJSONSidecar(dir, name string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	return os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644)
}

// --- constraints -> Calliope 0.7 add_math ------------------------------------

// calliopeVarInfo maps the canonical constraint variables to Calliope decision
// variables and the dimensions summed over to reduce them to a scalar.
var calliopeVarInfo = map[model.Variable]struct {
	name string
	dims []string
}{model.VarCapacity: {"flow_cap", []string{"nodes", "techs", "carriers"}}, model.VarFlowOut: {"flow_out", []string{"nodes", "techs", "carriers", "timesteps"}}, model.VarFlowIn: {"flow_in", []string{"nodes", "techs", "carriers", "timesteps"}}, model.VarStorageCap: {"storage_cap", []string{"nodes", "techs"}}, model.VarEmissions: {"cost", []string{"nodes", "techs", "costs"}}}

// calliopeMathDoc assembles additional_math.yaml from three independent
// sources: the portable constraint IR + emission limits (when custom math is
// enabled for the experiment), ratio pinning for multi-port conversions (with a
// balance_conversion override masking those techs from the built-in sum
// balance, which would otherwise leave the split flexible and double-count the
// fuel), and the piecewise-performance machinery (breakpoints dimension,
// piecewise_x/piecewise_y parameters, one piecewise_constraints entry).
func calliopeMathDoc(m *model.Model, ms *calliopeMathState, renderPortable bool) *yamlNode {
	root := newYAML()
	cons := newYAML()
	if renderPortable {
		calliopePortableConstraints(cons, m.Constraints, m.EmissionLimits)
	}

	for _, ft := range ms.flowTechs {
		for _, f := range m.Technologies[ft.id].Flows {
			if f.Direction == model.FlowIn && f.Carrier == ft.ref {
				continue // the reference input is what everything is pinned to
			}
			varName := "flow_out"
			if f.Direction == model.FlowIn {
				varName = "flow_in"
			}
			expr := fmt.Sprintf("%s[techs=%s, carriers=%s] == %s * flow_in[techs=%s, carriers=%s]",
				varName, ft.id, f.Carrier, emit.Ftoa(f.Ratio), ft.id, ft.ref)
			n := newYAML()
			n.set("description", "energymodel fixed flow ratio")
			n.set("foreach", []any{"nodes", "timesteps"})
			n.set("where", fmt.Sprintf("defined(techs=[%s], within=nodes, how=all)", ft.id))
			eq := newYAML()
			eq.set("expression", expr)
			n.set("equations", []any{eq})
			cons.set(fmt.Sprintf("meme_ratio_%s_%s_%s", ft.id, f.Direction, f.Carrier), n)
		}
	}

	// Mask the built-in carrier-sum balance for techs whose balance the math
	// above (ratio pinning) or below (piecewise curve) defines instead.
	if len(ms.flowTechs) > 0 || len(ms.pwTechs) > 0 {
		where := "base_tech==conversion AND NOT include_storage==true"
		if len(ms.flowTechs) > 0 {
			ids := make([]string, len(ms.flowTechs))
			for i, ft := range ms.flowTechs {
				ids[i] = ft.id
			}
			where += " AND NOT [" + strings.Join(ids, ", ") + "] in techs"
		}
		if len(ms.pwTechs) > 0 {
			where += " AND NOT piecewise_x"
		}
		n := newYAML()
		n.set("description", "Fix the relationship between a `conversion` technology's outflow and consumption.")
		n.set("foreach", []any{"nodes", "techs", "timesteps"})
		n.set("where", where)
		eq := newYAML()
		eq.set("expression", "sum(flow_out_inc_eff, over=carriers) == sum(flow_in_inc_eff, over=carriers)")
		n.set("equations", []any{eq})
		cons.set("balance_conversion", n)
	}
	if cons.len() > 0 {
		root.set("constraints", cons)
	}

	if len(ms.pwTechs) > 0 {
		dims := newYAML()
		bp := newYAML()
		bp.set("description", "piecewise breakpoint index")
		bp.set("dtype", "integer")
		dims.set("breakpoints", bp)
		root.set("dimensions", dims)

		params := newYAML()
		px := newYAML()
		px.set("description", "piecewise inflow values at the breakpoints")
		params.set("piecewise_x", px)
		py := newYAML()
		py.set("description", "piecewise outflow values at the breakpoints")
		params.set("piecewise_y", py)
		root.set("parameters", params)

		pwc := newYAML()
		c := newYAML()
		c.set("description", "energymodel piecewise part-load conversion performance")
		c.set("foreach", []any{"nodes", "techs", "timesteps"})
		c.set("where", "piecewise_x AND piecewise_y")
		c.set("x_expression", "sum(flow_in, over=carriers)")
		c.set("x_values", "piecewise_x")
		c.set("y_expression", "sum(flow_out, over=carriers)")
		c.set("y_values", "piecewise_y")
		pwc.set("meme_piecewise_efficiency", c)
		root.set("piecewise_constraints", pwc)
	}
	return root
}

// calliopePortableConstraints renders the portable constraint IR + emission
// limits into `constraints:` entries.
func calliopePortableConstraints(cons *yamlNode, constraints []model.Constraint, limits []model.EmissionLimit) {
	for i, c := range constraints {
		name := c.Name
		if name == "" {
			name = fmt.Sprintf("constraint_%d", i+1)
		}
		cons.set(name, calliopeConstraintNode(c))
	}
	for i, el := range limits {
		name := el.Name
		if name == "" {
			name = fmt.Sprintf("emission_limit_%d", i+1)
		}
		attr := el.Carrier
		if attr == "" {
			attr = "co2"
		}
		expr := fmt.Sprintf("sum(cost[costs=%s], over=[nodes, techs, costs]) %s %s", attr, el.Sense, emit.Ftoa(el.Limit))
		cons.set(name, mathConstraintNode(expr, "energymodel emission limit"))
	}
}

func calliopeConstraintNode(c model.Constraint) *yamlNode {
	parts := make([]string, 0, len(c.Terms))
	for _, t := range c.Terms {
		parts = append(parts, calliopeTerm(t))
	}
	expr := strings.Join(parts, " + ") + " " + c.Sense + " " + emit.Ftoa(c.Bound)
	return mathConstraintNode(expr, "energymodel linear constraint")
}

func mathConstraintNode(expr, desc string) *yamlNode {
	n := newYAML()
	n.set("description", desc)
	eq := newYAML()
	eq.set("expression", expr)
	n.set("equations", []any{eq})
	return n
}

func calliopeTerm(t model.Term) string {
	info, ok := calliopeVarInfo[t.Variable]
	name := info.name
	dims := info.dims
	if !ok {
		name = string(t.Variable)
		dims = []string{"nodes", "techs"}
	}

	// Collect the dimensions this term slices and their requested members.
	// Calliope 0.7 slices ONE member per dimension (`flow_cap[techs=pv]`); it has
	// no inline list slicing, so multi-member sets expand into a sum of
	// single-member-sliced terms below.
	type dimSlice struct {
		dim     string
		members []string
	}
	var sliced []dimSlice
	if len(t.Techs) > 0 {
		sliced = append(sliced, dimSlice{"techs", t.Techs})
	}
	if t.Variable == model.VarEmissions {
		cc := "co2"
		if len(t.Carriers) == 1 {
			cc = t.Carriers[0]
		}
		sliced = append(sliced, dimSlice{"costs", []string{cc}})
	} else if len(t.Carriers) > 0 {
		sliced = append(sliced, dimSlice{"carriers", t.Carriers})
	}
	if len(t.Nodes) > 0 {
		sliced = append(sliced, dimSlice{"nodes", t.Nodes})
	}

	// Cartesian product of the sliced dimensions -> one single-member slice map
	// per combination.
	combos := [][]string{{}}
	for _, s := range sliced {
		var next [][]string
		for _, prefix := range combos {
			for _, m := range s.members {
				next = append(next, append(append([]string{}, prefix...), s.dim+"="+m))
			}
		}
		combos = next
	}

	over := strings.Join(dims, ", ")
	parts := make([]string, 0, len(combos))
	for _, c := range combos {
		slice := ""
		if len(c) > 0 {
			slice = "[" + strings.Join(c, ", ") + "]"
		}
		parts = append(parts, fmt.Sprintf("sum(%s%s, over=[%s])", name, slice, over))
	}
	term := strings.Join(parts, " + ")
	if len(parts) > 1 {
		term = "(" + term + ")"
	}
	if t.Coefficient != 1 {
		term = emit.Ftoa(t.Coefficient) + " * " + term
	}
	return term
}
