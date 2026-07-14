// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package adoptnet0

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

const adoptPeriod = "period1"

// Emitter writes an AdOpT-NET0 input_data tree that adopt_net0 reads and
// solves. It matches the framework's actual on-disk schema (lowercase .json,
// semicolon-delimited timestep CSVs, a compact ConfigModel, and the scaffolding
// files adopt_net0 requires) rather than an idealized shape.
//
// Technologies are database-driven in adopt_net0 (a tech is a named library
// entry copied via copy_technology_data), so the emitter lists the mapped
// database tech names in Technologies.json and writes an economics-override
// sidecar; the orchestrator's run step copies the tech data and applies the
// overrides before solving (see internal/service).
type Emitter struct{}

// Emit writes the input_data tree (plus the results scaffold adopt_net0
// requires) under outDir and returns the tree root as the entrypoint.
func (Emitter) Emit(j *model.Job, outDir string) (string, error) {
	m := &j.Model
	root := filepath.Join(outDir, "input_data")
	if err := emit.Mkdirs(root); err != nil {
		return "", err
	}
	// adopt_net0 requires an existing results directory (save path).
	resDir := filepath.Join(outDir, "results")
	if err := emit.Mkdirs(resDir); err != nil {
		return "", err
	}

	ts := adoptTimeIndex(m.Time)

	// Topology.json -----------------------------------------------------------
	topo := map[string]any{
		"nodes":                    emit.Keys(m.Nodes),
		"carriers":                 emit.Keys(m.Carriers),
		"investment_periods":       []string{adoptPeriod},
		"start_date":               strings.TrimSuffix(ts[0], ":00"), // YYYY-MM-DD HH:MM
		"end_date":                 strings.TrimSuffix(ts[len(ts)-1], ":00"),
		"resolution":               adoptResolution(m.Time.Resolution),
		"investment_period_length": 1,
	}
	if err := emit.WriteJSON(root, "Topology.json", topo); err != nil {
		return "", err
	}

	// ConfigModel.json (compact; only the value fields adopt_net0 reads) -------
	cfg := adoptConfig(j, resDir)
	if err := emit.MergeNativeMap(cfg, m.Native.For(model.TargetAdOpt)); err != nil {
		return "", err
	}
	if err := emit.WriteJSON(root, "ConfigModel.json", cfg); err != nil {
		return "", err
	}

	// NodeLocations.csv (semicolon; lon/lat/alt required) ---------------------
	loc := [][]string{{"", "lon", "lat", "alt"}}
	for _, id := range emit.Keys(m.Nodes) {
		lon, lat, alt := "0", "0", "0"
		if c := m.Nodes[id].Coords; c != nil {
			lon, lat, alt = emit.Ftoa(c.Lon), emit.Ftoa(c.Lat), emit.Ftoa(c.Alt)
		}
		loc = append(loc, []string{id, lon, lat, alt})
	}
	if err := writeSemicolonCSV(root, "NodeLocations.csv", loc); err != nil {
		return "", err
	}

	// period folder -----------------------------------------------------------
	period := filepath.Join(root, adoptPeriod)
	if err := emit.WriteJSON(period, "Networks.json", map[string]any{
		"existing": map[string]any{}, "new": []string{},
	}); err != nil {
		return "", err
	}
	overrides := map[string]map[string]any{} // node -> tech -> economics/size overrides for the runner
	for _, nid := range emit.Keys(m.Nodes) {
		if err := adoptNode(m, nid, filepath.Join(period, "node_data", nid), ts, overrides); err != nil {
			return "", err
		}
	}
	// Sidecar consumed by the run step after copy_technology_data.
	if err := emit.WriteJSON(root, "_meme_overrides.json", overrides); err != nil {
		return "", err
	}
	return root, nil
}

func adoptNode(m *model.Model, nid, dir string, ts []string, overrides map[string]map[string]any) error {
	carrierDir := filepath.Join(dir, "carrier_data")
	if err := emit.Mkdirs(carrierDir); err != nil {
		return err
	}

	// Technologies.json: map supported techs to database names; unmapped techs
	// are skipped (they can be added via native.adopt-net0).
	newTechs := []string{}
	existing := map[string]any{}
	for _, tid := range emit.Keys(m.Technologies) {
		t := m.Technologies[tid]
		if !model.ContainsStr(t.Node, nid) || t.Role == model.RoleDemand || !t.IsActive() {
			continue
		}
		dbName, ok := dbTech(t)
		if !ok {
			continue
		}
		e := t.At(nid)
		if e.Capacity != nil && !e.Capacity.Expandable && e.Capacity.Existing > 0 {
			existing[dbName] = e.Capacity.Existing
		} else {
			newTechs = append(newTechs, dbName)
		}
		ov := adoptOverrides(e)
		// Tech-level native.adopt-net0 is patched onto the copied database tech.
		if err := emit.MergeNativeMap(ov, e.Native.For(model.TargetAdOpt)); err != nil {
			return fmt.Errorf("tech %q: %w", tid, err)
		}
		if len(ov) > 0 {
			// Keyed per node: the same database tech can carry different
			// overrides at different nodes (node_overrides).
			if overrides[nid] == nil {
				overrides[nid] = map[string]any{}
			}
			overrides[nid][dbName] = ov
		}
	}
	sort.Strings(newTechs)
	techs := map[string]any{"existing": existing, "new": newTechs}
	if err := emit.MergeNativeMap(techs, m.Nodes[nid].Native.For(model.TargetAdOpt)); err != nil {
		return fmt.Errorf("node %q: %w", nid, err)
	}
	if err := emit.WriteJSON(dir, "Technologies.json", techs); err != nil {
		return err
	}
	// Pre-create the (empty) technology_data dir: the run step's
	// copy_technology_data uses shutil.copy, which needs the target dir to exist.
	if len(newTechs) > 0 || len(existing) > 0 {
		if err := emit.Mkdirs(filepath.Join(dir, "technology_data")); err != nil {
			return err
		}
	}

	// EnergybalanceOptions.json -----------------------------------------------
	ebo := map[string]any{}
	for _, car := range emit.Keys(m.Carriers) {
		ebo[car] = map[string]any{"curtailment_possible": 0}
	}
	if err := emit.WriteJSON(carrierDir, "EnergybalanceOptions.json", ebo); err != nil {
		return err
	}

	// CarbonCost.csv (scaffolding; empty values) ------------------------------
	if err := writeAdoptTimeCSV(dir, "CarbonCost.csv", ts, []string{"price", "subsidy"}, nil); err != nil {
		return err
	}
	// ClimateData.csv: materialized from node.climate weather series (empty when
	// absent). Physics techs (Photovoltaic, wind) read these to compute output.
	climCols := []string{"ghi", "dni", "dhi", "temp_air", "rh", "ws10", "hydro_inflow"}
	if err := writeAdoptTimeCSV(dir, "ClimateData.csv", ts, climCols, adoptClimate(m, nid, climCols, ts)); err != nil {
		return err
	}

	// carrier_data/<carrier>.csv (demand + trade) -----------------------------
	cols := []string{"Demand", "Import limit", "Export limit", "Import price",
		"Export price", "Import emission factor", "Export emission factor", "Generic production"}
	for _, car := range emit.Keys(m.Carriers) {
		series := map[string][]string{}
		set := func(col, val string) {
			if val == "" {
				return
			}
			s := make([]string, len(ts))
			for i := range s {
				s[i] = val
			}
			series[col] = s
		}
		for _, tid := range emit.Keys(m.Technologies) {
			t := m.Technologies[tid]
			if t.Role == model.RoleDemand && model.ContainsStr(t.Node, nid) && t.CarrierIn.First() == car && t.IsActive() {
				series["Demand"] = adoptDemand(m, t, ts)
			}
		}
		for _, trID := range emit.Keys(m.Trade) {
			tr := m.Trade[trID]
			if tr.Node != nid || tr.Carrier != car {
				continue
			}
			if tr.Import != nil {
				if tr.Import.Limit != nil {
					// Scalar or series: adopt's Import limit column is
					// per-timestep, so both materialize exactly.
					series["Import limit"] = adoptValueColumn(m, tr.Import.Limit, ts)
				}
				series["Import price"] = adoptValueColumn(m, tr.Import.Price, ts)
				set("Import emission factor", emit.OptFloat(tr.Import.EmissionFactor, ""))
			}
			if tr.Export != nil {
				if tr.Export.Limit != nil {
					series["Export limit"] = adoptValueColumn(m, tr.Export.Limit, ts)
				}
				series["Export price"] = adoptValueColumn(m, tr.Export.Price, ts)
				set("Export emission factor", emit.OptFloat(tr.Export.EmissionFactor, ""))
			}
		}
		if err := writeAdoptTimeCSV(carrierDir, car+".csv", ts, cols, series); err != nil {
			return err
		}
	}
	return nil
}

// adoptClimate materializes a node's weather series onto the timeindex, one
// column per adopt climate variable present in node.climate (scalar or series
// ref). Absent columns are left blank.
func adoptClimate(m *model.Model, nid string, cols []string, ts []string) map[string][]string {
	clim := m.Nodes[nid].Climate
	if len(clim) == 0 {
		return nil
	}
	out := map[string][]string{}
	for _, col := range cols {
		v, ok := clim[col]
		if !ok {
			continue
		}
		s := make([]string, len(ts))
		if v.IsSeries() {
			vals, _ := emit.SeriesValues(m, v.SeriesID())
			for i := range ts {
				s[i] = emit.Ftoa(emit.SeriesAt(vals, i))
			}
		} else if f, ok := v.Reduce(); ok {
			for i := range ts {
				s[i] = emit.Ftoa(f)
			}
		}
		out[col] = s
	}
	return out
}

// adoptValueColumn materializes any scalar-or-series Value onto the timeindex
// (adopt carrier CSVs are per-timestep, so a series price maps exactly).
func adoptValueColumn(m *model.Model, v *model.Value, ts []string) []string {
	out := make([]string, len(ts))
	if v.IsSeries() {
		vals, _ := emit.SeriesValues(m, v.SeriesID())
		for i := range ts {
			out[i] = emit.Ftoa(emit.SeriesAt(vals, i))
		}
		return out
	}
	s := emit.ValScalar(v, 0)
	for i := range ts {
		out[i] = s
	}
	return out
}

// adoptDemand materializes a demand profile onto the timeindex (scalar or series).
func adoptDemand(m *model.Model, t model.Technology, ts []string) []string {
	out := make([]string, len(ts))
	if t.DemandProfile.IsSeries() {
		vals, _ := emit.SeriesValues(m, t.DemandProfile.SeriesID())
		for i := range ts {
			out[i] = emit.Ftoa(emit.SeriesAt(vals, i))
		}
		return out
	}
	v := emit.ScalarOr(t.DemandProfile, 0)
	for i := range ts {
		out[i] = v
	}
	return out
}

// adoptConfig builds the compact ConfigModel adopt_net0 reads (value fields
// only). Objective maps to adopt's names; the solver is glpk (the free solver
// adopt supports — it accepts only gurobi or glpk, not HiGHS/cbc).
func adoptConfig(j *model.Job, resDir string) map[string]any {
	objective := "costs"
	if j.Experiment.Objective == model.ObjectiveMinEmissions {
		// emission_accounting selects adopt's net vs positive-only objective.
		if j.Experiment.EmissionAccounting == "positive_only" {
			objective = "emissions_pos"
		} else {
			objective = "emissions_net"
		}
	}
	limit := 0.0
	if len(j.Model.EmissionLimits) > 0 {
		objective = "costs_emissionlimit"
		limit = j.Model.EmissionLimits[0].Limit
	}

	// Run mode drives adopt's own solve configuration. Pareto and Monte Carlo are
	// NOT separate run entrypoints in adopt_net0 — a single quick_solve() branches
	// on ConfigModel: objective "pareto" builds the cost/emission trade-off front,
	// and monte_carlo.N > 0 triggers the sampling loop. Without translating the
	// canonical mode here the capability matrix would accept the payload but the
	// emitted config would silently solve a plain cost-optimal plan, so map it.
	mcN := 0
	mcSD := 0.2
	mcOn := []string{"Technologies"}
	switch j.Experiment.EffectiveMode() {
	case model.ModePareto:
		objective = "pareto"
	case model.ModeMonteCarlo:
		// First-class experiment.monte_carlo wins over the legacy
		// solver.options.monte_carlo namespace.
		mcN = adoptOptionInt(j.Experiment.Solver.Options, "monte_carlo", "N", 10)
		if mc := j.Experiment.MonteCarlo; mc != nil {
			if mc.Samples > 0 {
				mcN = mc.Samples
			}
			if mc.StandardDeviation != nil {
				mcSD = *mc.StandardDeviation
			}
			if len(mc.On) > 0 {
				mcOn = adoptOnWhat(mc.On)
			}
		}
	}
	// Time aggregation -> typical-days clustering (adopt drives TSAM). N is the
	// number of typical days; 0 leaves the full time series intact.
	tdN := 0
	fullRes := []string{"RES", "STOR", "Hydro_Open"}
	if ta := j.Experiment.TimeAggregation; ta != nil && ta.Method == "typical_days" {
		if tdN = ta.Periods; tdN <= 0 {
			tdN = 10
		}
		if len(ta.KeepFullResolutionFor) > 0 {
			fullRes = ta.KeepFullResolutionFor
		}
	}
	// Energy-balance switches: unmet-demand pricing and the copperplate mode.
	violation := -1.0
	if ud := j.Experiment.AllowUnmetDemand; ud != nil && ud.Enabled && ud.PenaltyPrice != nil {
		violation = *ud.PenaltyPrice
	}
	copperplate := 0
	if j.Experiment.Copperplate {
		copperplate = 1
	}
	caseName := any(-1)
	if r := j.Experiment.Reporting; r != nil && r.CaseName != "" {
		caseName = r.CaseName
	}
	discount := -1.0
	if j.Model.DiscountRate != nil {
		discount = *j.Model.DiscountRate
	}
	// Technology dynamics (max_startups, standby_power) take effect only when
	// ConfigModel.performance.dynamics is enabled; switch it on exactly when a
	// technology carries such a field.
	dynamics := 0
	for _, t := range j.Model.Technologies {
		if op := t.Operation; op != nil && (op.MaxStartups != nil || op.StandbyPower != nil) {
			dynamics = 1
			break
		}
	}
	paretoPoints := adoptOptionInt(j.Experiment.Solver.Options, "pareto", "points", 5)
	if p := j.Experiment.Pareto; p != nil && p.Points > 0 {
		paretoPoints = p.Points
	}
	// Solver tuning: canonical seconds -> adopt hours; defaults match the
	// previous hardcoded values.
	mipgap, timelimH, threads := 0.001, 10.0, 0
	if s := j.Experiment.Solver; true {
		if s.MIPGap != nil {
			mipgap = *s.MIPGap
		}
		if s.TimeLimit != nil {
			timelimH = *s.TimeLimit / 3600
		}
		if s.Threads != nil {
			threads = *s.Threads
		}
	}

	val := func(v any) map[string]any { return map[string]any{"value": v} }
	return map[string]any{
		"optimization": map[string]any{
			"objective": val(objective), "emission_limit": val(limit),
			"monte_carlo": map[string]any{
				"N": val(mcN), "type": val("normal_dis"), "sd": val(mcSD),
				"on_what": val(mcOn),
			},
			"typicaldays": map[string]any{
				"N": val(tdN), "method": val(2),
				"technologies_with_full_res": val(fullRes),
			},
			"multiyear": val(0), "timestaging": val(0), "pareto_points": val(paretoPoints),
		},
		"solveroptions": map[string]any{
			"solver": val("glpk"), "mipgap": val(mipgap), "timelim": val(timelimH),
			"threads": val(threads), "nodetol": val(1e-9),
		},
		"reporting": map[string]any{
			"save_path": val(resDir), "save_summary_path": val(resDir),
			// write_results=1: adopt writes the H5 results + Summary.xlsx after each
			// optimization. Monte-Carlo runs *require* it (add_values_to_summary
			// reads Summary.xlsx unconditionally), and the result bundle must carry
			// the solver results anyway.
			"write_results": val(1), "case_name": val(caseName), "write_solution_diagnostics": val(0),
		},
		"energybalance": map[string]any{"copperplate": val(copperplate), "violation": val(violation)},
		"economic":      map[string]any{"global_discountrate": val(discount), "global_simple_capex_model": val(0)},
		"performance":   map[string]any{"dynamics": val(dynamics)},
		"scaling": map[string]any{
			"scaling_on": val(0),
			"scaling_factors": map[string]any{
				"energy_vars": val(0.001), "cost_vars": val(0.001), "objective": val(1),
			},
		},
	}
}

// adoptOnWhat maps canonical monte_carlo.on classes onto adopt's on_what names.
// Unknown entries were already rejected by Experiment.Validate.
func adoptOnWhat(on []string) []string {
	names := map[string]string{
		"technology_capex": "Technologies",
		"network_capex":    "Networks",
		"import_price":     "Import",
		"export_price":     "Export",
	}
	out := make([]string, 0, len(on))
	for _, o := range on {
		if n, ok := names[o]; ok {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return []string{"Technologies"}
	}
	return out
}

// adoptOptionInt reads experiment.solver.options[group][key] as an integer,
// falling back to def. JSON numbers decode as float64, so both float64 and int
// are accepted. It lets a payload tune the run (e.g. monte_carlo iterations,
// pareto points) without a first-class field, mirroring how the Calliope emitter
// reads options["spores"]/options["operate"].
func adoptOptionInt(opts map[string]any, group, key string, def int) int {
	raw, ok := opts[group]
	if !ok {
		return def
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return def
	}
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

// adoptOverrides collects the economics/size fields to patch onto a copied
// database tech (applied by the run step after copy_technology_data).
func adoptOverrides(t model.Technology) map[string]any {
	ov := map[string]any{}
	if max := t.Capacity.EffectiveMax(); max != nil {
		ov["size_max"] = *max
	}
	if min := t.Capacity.EffectiveMin(); min != nil {
		ov["size_min"] = *min
	}
	if t.Capacity != nil && t.Capacity.DecommissionMode() != "impossible" {
		ov["decommission"] = t.Capacity.DecommissionMode()
	}
	econ := map[string]any{}
	if c, ok := t.Costs[model.PrimaryCostClass]; ok {
		if c.InvestmentPerCapacity != nil {
			econ["unit_CAPEX"] = *c.InvestmentPerCapacity
		}
		if c.FixedOM != nil {
			econ["OPEX_fixed"] = *c.FixedOM
		}
		// The fraction-of-CAPEX convention IS adopt's OPEX_fixed semantics
		// (mutual exclusion with FixedOM is validated upstream).
		if c.FixedOMFraction != nil {
			econ["OPEX_fixed"] = *c.FixedOMFraction
		}
		if v, ok := c.VariableOM.Reduce(); ok {
			econ["OPEX_variable"] = v
		}
	}
	if t.Lifetime != nil {
		econ["lifetime"] = *t.Lifetime
	}
	if t.InterestRate != nil {
		econ["discount_rate"] = *t.InterestRate
	}
	if t.Capacity != nil && t.Capacity.Decommission != nil && t.Capacity.Decommission.Cost != nil {
		econ["decommission_cost"] = *t.Capacity.Decommission.Cost
	}
	if len(econ) > 0 {
		ov["Economics"] = econ
	}
	perf := map[string]any{}
	if t.EmissionFactor != nil {
		perf["emission_factor"] = *t.EmissionFactor
	}
	if op := t.Operation; op != nil {
		if op.MaxStartups != nil {
			perf["max_startups"] = *op.MaxStartups
		}
		if op.StandbyPower != nil {
			perf["standby_power"] = *op.StandbyPower
		}
	}
	if s := t.Storage; s != nil {
		// Independent charge/discharge sizing patches the database tech's
		// Flexibility block (fractions of energy capacity per hour, adopt's
		// own convention).
		flex := map[string]any{}
		if s.MaxChargeRate != nil {
			flex["charge_rate"] = *s.MaxChargeRate
		}
		if s.MaxDischargeRate != nil {
			flex["discharge_rate"] = *s.MaxDischargeRate
		}
		if len(flex) > 0 {
			ov["Flexibility"] = flex
		}
		if s.NoSimultaneousChargeDischarge {
			perf["allow_only_one_direction"] = 1
		}
	}
	if len(perf) > 0 {
		ov["Performance"] = perf
	}
	return ov
}

// ---------------------------------------------------------------------------
// Time index + CSV helpers (adopt_net0 uses a pandas date_range and ';' CSVs).
// ---------------------------------------------------------------------------

// adoptTimeIndex builds the inclusive [start, end] timestamp list at the model
// resolution, matching pandas date_range(start, end, freq). Falls back to a
// single step if the dates can't be parsed.
func adoptTimeIndex(tc model.TimeConfig) []string {
	start, ok := emit.ParseDate(tc.Start)
	if !ok {
		return []string{"2022-01-01 00:00:00"}
	}
	end, ok := emit.ParseDate(tc.End)
	step := emit.ParseHours(tc.Resolution)
	if !ok || step <= 0 || !end.After(start) {
		return []string{start.Format("2006-01-02 15:04:05")}
	}
	var out []string
	for t := start; !t.After(end); t = t.Add(step) {
		out = append(out, t.Format("2006-01-02 15:04:05"))
	}
	return out
}

// adoptResolution converts a canonical resolution ("1H") to adopt's ("1h").
func adoptResolution(res string) string {
	if res == "" {
		return "1h"
	}
	return strings.ToLower(res)
}

// writeAdoptTimeCSV writes a semicolon-delimited, timestep-indexed CSV: the
// header is ";col1;col2;…" (empty index name) and each row is "ts;v1;v2;…".
// series[col] supplies per-timestep values; absent columns are blank.
func writeAdoptTimeCSV(dir, name string, ts []string, cols []string, series map[string][]string) error {
	var b strings.Builder
	b.WriteString(";" + strings.Join(cols, ";") + "\n")
	for i, t := range ts {
		b.WriteString(t)
		for _, c := range cols {
			b.WriteByte(';')
			if s, ok := series[c]; ok && i < len(s) {
				b.WriteString(s[i])
			}
		}
		b.WriteByte('\n')
	}
	if err := emit.Mkdirs(dir); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644)
}

// writeSemicolonCSV writes a plain semicolon-delimited CSV (header + rows).
func writeSemicolonCSV(dir, name string, rows [][]string) error {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Join(r, ";"))
		b.WriteByte('\n')
	}
	if err := emit.Mkdirs(dir); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644)
}
