// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

// Calliope 0.6 model-definition vocabulary used by Emitter06.
const (
	v06TimeseriesDir   = "timeseries_data" // model.timeseries_data_path
	v06EmissionClass   = "emissions"       // cost class carrying emissions (0.6 convention)
	v06SporesClass     = "spores_score"    // SPORES scoring cost class (upstream default)
	v06SporesCostGroup = "systemwide_cost_max"
	v06RandomSeed      = 23 // model.random_seed for reproducible k-means clustering
)

// Emitter06 writes a Calliope v0.6 model: one model.yaml with the `model`,
// `run`, `techs`, `locations`, `links` and `group_constraints` sections, plus
// the timeseries_data/ CSVs its `file=` references point at (demand profiles
// are negative, as 0.6 requires). Portable constraints and emission limits
// become group_constraints; multi-output conversion becomes conversion_plus
// carrier tiers with carrier_ratios; trade becomes supply/demand market techs.
type Emitter06 struct{}

// csv06 is one timeseries_data/ file: a datetime index plus one column per
// node, referenced as `file=<file>:<node>`.
type csv06 struct {
	file    string
	nodes   []string
	columns map[string][]float64
}

// state06 collects, across the tech loop, what the run block and the group
// constraints need to know.
type state06 struct {
	csvs      map[string]*csv06
	csvOrder  []string
	anyEmis   bool // any tech/trade/link carries an emissions cost
	cyclic    int  // storage techs with cyclic SOC
	nonCyclic int  // storage techs without
	groups    *yamlNode
	spores    bool
}

func (st *state06) column(file, node string, values []float64) {
	c, ok := st.csvs[file]
	if !ok {
		c = &csv06{file: file, columns: map[string][]float64{}}
		st.csvs[file] = c
		st.csvOrder = append(st.csvOrder, file)
	}
	if _, dup := c.columns[node]; !dup {
		c.nodes = append(c.nodes, node)
	}
	c.columns[node] = values
}

// Emit writes model.yaml and the CSVs under outDir and returns the model.yaml
// path as the entrypoint.
func (Emitter06) Emit(j *model.Job, outDir string) (string, error) {
	m := &j.Model
	e := &j.Experiment
	mode := e.EffectiveMode()
	tsDir := filepath.Join(outDir, v06TimeseriesDir)
	if err := os.MkdirAll(tsDir, 0o755); err != nil {
		return "", err
	}
	st := &state06{csvs: map[string]*csv06{}, groups: newYAML(), spores: mode == model.ModeAlternatives}
	root := newYAML()

	// model -------------------------------------------------------------------
	mdl := newYAML()
	name := m.Metadata.Name
	if name == "" {
		name = "energymodel"
	}
	mdl.set("name", name)
	mdl.set("calliope_version", calliope06Version)
	mdl.set("timeseries_data_path", v06TimeseriesDir)
	if len(m.Time.Subset) == 2 {
		mdl.set("subset_time", []any{m.Time.Subset[0], m.Time.Subset[1]})
	}
	if ta := e.TimeAggregation; ta != nil {
		tm := newYAML()
		opts := newYAML()
		switch ta.Method {
		case "resample":
			tm.set("function", "resample")
			opts.set("resolution", strings.ToUpper(ta.Resolution))
		case "typical_days":
			// k-means representative days (0.6 built-in clustering).
			tm.set("function", "apply_clustering")
			opts.set("clustering_func", "kmeans")
			opts.set("how", "mean")
			opts.set("k", ta.Periods)
			mdl.set("random_seed", v06RandomSeed)
		}
		tm.set("function_options", opts)
		mdl.set("time", tm)
	}
	root.set("model", mdl)
	root.set("run", nil) // filled in below, once the cost classes are known

	// techs + per-location placement -------------------------------------------
	techs := newYAML()
	locTechs := map[string]*yamlNode{}
	place := func(node, tech string, override *yamlNode) {
		lt, ok := locTechs[node]
		if !ok {
			lt = newYAML()
			locTechs[node] = lt
		}
		lt.set(tech, override)
	}
	for _, id := range emit.Keys(m.Technologies) {
		t := m.Technologies[id]
		tn, err := tech06(t.At(t.Node.First()), m, id, st)
		if err != nil {
			return "", err
		}
		if mode == model.ModeOperate {
			applyOperateCaps06(tn, t.At(t.Node.First()))
		}
		techs.set(id, tn)
		for _, node := range t.Node {
			place(node, id, locationTech06(t, node, id, m, st))
		}
	}
	links := newYAML()
	for _, id := range emit.Keys(m.Transmission) {
		l := m.Transmission[id]
		tn, link, err := transmission06(l, id, st)
		if err != nil {
			return "", err
		}
		if mode == model.ModeOperate {
			if v, ok := fixedCap(l.Capacity); ok {
				operateCap06(tn, v)
			}
		}
		techs.set(id, tn)
		key := l.From + "," + l.To
		ln, ok := links.vals[key].(*yamlNode)
		if !ok {
			ln = newYAML()
			ln.set("techs", newYAML())
			links.set(key, ln)
		}
		ln.vals["techs"].(*yamlNode).set(id, link)
	}
	for _, id := range emit.Keys(m.Trade) {
		tr := m.Trade[id]
		if tr.Import != nil {
			tn, err := tradeImport06(tr, id, st)
			if err != nil {
				return "", err
			}
			if mode == model.ModeOperate {
				if f, ok := tr.Import.Limit.Reduce(); ok {
					operateCap06(tn, f)
				}
			}
			techs.set(id+"_import", tn)
			place(tr.Node, id+"_import", nil)
		}
		if tr.Export != nil {
			tn, err := tradeExport06(tr, id, st)
			if err != nil {
				return "", err
			}
			techs.set(id+"_export", tn)
			place(tr.Node, id+"_export", nil)
		}
	}
	root.set("techs", techs)

	// locations ---------------------------------------------------------------
	allCoords := allNodesHaveCoords(m)
	locs := newYAML()
	for _, id := range emit.Keys(m.Nodes) {
		n := m.Nodes[id]
		ln := newYAML()
		if allCoords {
			co := newYAML()
			co.set("lat", yamlFloat(n.Coords.Lat))
			co.set("lon", yamlFloat(n.Coords.Lon))
			ln.set("coordinates", co)
		}
		if n.AvailableArea != nil {
			ln.set("available_area", *n.AvailableArea)
		}
		// Every location must list its techs, even as an empty mapping (a hub
		// only crossed by transmission links).
		lt, ok := locTechs[id]
		if !ok {
			lt = newYAML()
		}
		ln.set("techs", lt)
		if err := mergeNativeDeepIntoYAML(ln, n.Native.For(model.TargetCalliope06)); err != nil {
			return "", fmt.Errorf("node %q: %w", id, err)
		}
		locs.set(id, ln)
	}
	root.set("locations", locs)
	if links.len() > 0 {
		root.set("links", links)
	}

	// group_constraints: emission limits, portable constraints, per-tech
	// annual output bounds and systemwide minima (collected in st.groups by
	// the tech loop), plus the SPORES cost-slack group.
	for i, el := range m.EmissionLimits {
		gname := el.Name
		if gname == "" {
			gname = fmt.Sprintf("emission_limit_%d", i+1)
		}
		g := newYAML()
		g.set("cost_"+senseSuffix06(el.Sense), classValue06(v06EmissionClass, el.Limit))
		st.groups.set(gname, g)
		st.anyEmis = true
	}
	for i, c := range m.Constraints {
		g, err := groupConstraint06(c, i, m)
		if err != nil {
			return "", err
		}
		st.groups.set(constraintName06(c, i), g)
	}
	if st.spores {
		g := newYAML()
		g.set("cost_max", classValue06(model.PrimaryCostClass, math.Inf(1)))
		st.groups.set(v06SporesCostGroup, g)
	}
	if st.groups.len() > 0 {
		root.set("group_constraints", st.groups)
	}

	// run ----------------------------------------------------------------------
	root.set("run", run06(e, st))

	// Model- and experiment-level native fragments extend the whole document
	// (e.g. run.bigM, model.random_seed, extra group_constraints).
	if err := mergeNativeDeepIntoYAML(root, m.Native.For(model.TargetCalliope06)); err != nil {
		return "", fmt.Errorf("model: %w", err)
	}
	if err := mergeNativeDeepIntoYAML(root, e.Native.For(model.TargetCalliope06)); err != nil {
		return "", fmt.Errorf("experiment: %w", err)
	}

	path := filepath.Join(outDir, "model.yaml")
	if err := os.WriteFile(path, []byte(yamlDoc(root)), 0o644); err != nil {
		return "", err
	}
	for _, file := range st.csvOrder {
		if err := writeCSV06(tsDir, st.csvs[file], m); err != nil {
			return "", err
		}
	}
	return path, nil
}

// run06 builds the `run` block: mode, solver (+ options), objective cost-class
// weights (every class the model defines, so emissions are tracked but not
// minimized unless the objective says so), the model-wide storage switch,
// feasibility slack, logging, and the operate / SPORES settings.
func run06(e *model.Experiment, st *state06) *yamlNode {
	mode := e.EffectiveMode()
	run := newYAML()
	run.set("mode", mode06(mode))
	run.set("solver", calliopeSolver(e.Solver.Name))
	if so := calliopeSolverOptions(e.Solver); so != nil {
		run.set("solver_options", so)
	}

	minEmis := e.Objective == model.ObjectiveMinEmissions
	weights := newYAML()
	if minEmis {
		weights.set(model.PrimaryCostClass, 0)
		weights.set(v06EmissionClass, 1)
	} else {
		weights.set(model.PrimaryCostClass, 1)
		if st.anyEmis {
			weights.set(v06EmissionClass, 0)
		}
	}
	if st.spores {
		weights.set(v06SporesClass, 0)
	}
	obj := newYAML()
	obj.set("cost_class", weights)
	obj.set("sense", "minimize")
	run.set("objective_options", obj)

	if st.cyclic+st.nonCyclic > 0 {
		// One switch for all storage techs; mixed settings fall back to
		// non-cyclic (ValidateJob warns), and operate mode cannot be cyclic.
		run.set("cyclic_storage", st.nonCyclic == 0 && mode != model.ModeOperate)
	}
	if ud := e.AllowUnmetDemand; ud != nil && ud.Enabled {
		run.set("ensure_feasibility", true)
	}
	if r := e.Reporting; r != nil && r.SaveLogs != "" {
		run.set("save_logs", r.SaveLogs)
	}

	if mode == model.ModeOperate {
		// Receding horizon in whole hours. Defaults < legacy
		// experiment.solver.options.operate < first-class experiment.operate.
		window, horizon := 12, 24
		if raw, ok := e.Solver.Options["operate"]; ok {
			if over, ok := raw.(map[string]any); ok {
				if v, ok := hours06(over["window"]); ok {
					window = v
				}
				if v, ok := hours06(over["horizon"]); ok {
					horizon = v
				}
			}
		}
		if o := e.Operate; o != nil {
			if o.Window != "" {
				window = int(emit.ParseHours(o.Window).Hours())
			}
			if o.Horizon != "" {
				horizon = int(emit.ParseHours(o.Horizon).Hours())
			}
		}
		op := newYAML()
		op.set("window", window)
		op.set("horizon", horizon)
		run.set("operation", op)
	}
	if st.spores {
		run.set("spores_options", spores06(e))
	}
	return run
}

// mode06 translates a canonical run mode into Calliope 0.6's run.mode name.
func mode06(m model.Mode) string {
	switch m {
	case model.ModeAlternatives:
		return "spores"
	case model.ModeOperate:
		return "operate"
	default:
		return "plan"
	}
}

// hours06 reads a legacy operate option given as a number of hours or a
// pandas frequency string.
func hours06(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case string:
		if x == "" {
			return 0, false
		}
		return int(emit.ParseHours(x).Hours()), true
	}
	return 0, false
}

// spores06 builds run.spores_options following the 0.6 SPORES recipe: the
// spores_score cost class is minimized against a monetary cost cap kept in the
// systemwide_cost_max group constraint. Defaults < legacy
// experiment.solver.options.spores (0.6 key names pass through) < first-class
// experiment.alternatives.
func spores06(e *model.Experiment) *yamlNode {
	sp := newYAML()
	sp.set("spores_number", 3)
	sp.set("slack", 0.1)
	sp.set("score_cost_class", v06SporesClass)
	sp.set("slack_cost_group", v06SporesCostGroup)
	ocw := newYAML()
	ocw.set(v06SporesClass, 1)
	ocw.set(model.PrimaryCostClass, 0)
	sp.set("objective_cost_class", ocw)
	if raw, ok := e.Solver.Options["spores"]; ok {
		if over, ok := raw.(map[string]any); ok {
			for _, k := range emit.SortedKeys(over) {
				switch k {
				case "number": // the 0.7 spelling, accepted for symmetry
					sp.set("spores_number", jsonToYAML(over[k]))
				default:
					sp.set(k, jsonToYAML(over[k]))
				}
			}
		}
	}
	if a := e.Alternatives; a != nil {
		if a.Number > 0 {
			sp.set("spores_number", a.Number)
		}
		if a.Slack != nil {
			sp.set("slack", *a.Slack)
		}
	}
	return sp
}

// tech06 renders one technology (as seen from its first node) into the 0.6
// essentials / constraints / costs structure.
func tech06(t model.Technology, m *model.Model, id string, st *state06) (*yamlNode, error) {
	ess, cons, costs := newYAML(), newYAML(), newYAML()
	ess.set("name", id)

	switch t.Role {
	case model.RoleSupply:
		parent := "supply"
		if t.Source != nil && t.Source.Cap != nil {
			parent = "supply_plus" // resource_cap_* exists only there
		}
		ess.set("parent", parent)
		ess.set("carrier_out", t.CarrierOut.First())
		if f, ok := emit.PerfEfficiency(t).Reduce(); ok {
			cons.set("energy_eff", f)
		}
		// Availability (equals_pu/max_pu or a physics precomputed profile) is
		// the resource per unit of energy_cap; a fixed dispatch forces the
		// whole resource to be used. Series values are placed per location.
		if av, force := availability06(t); av != nil {
			if f, ok := av.Reduce(); ok {
				cons.set("resource", f)
			}
			cons.set("resource_unit", "energy_per_cap")
			if force {
				cons.set("force_resource", true)
			}
		}
		// An explicit source block wins over the availability route.
		if s := t.Source; s != nil {
			if s.Max != nil {
				if f, ok := s.Max.Reduce(); ok {
					cons.set("resource", f)
				}
			}
			if s.Unit != "" {
				cons.set("resource_unit", resourceUnit06(s.Unit))
			}
			if s.Cap != nil {
				cons.set("resource_cap_max", *s.Cap)
			}
		}
		if a := t.Area; a != nil {
			if a.Max != nil {
				cons.set("resource_area_max", *a.Max)
			}
			if a.PerCapacity != nil {
				cons.set("resource_area_per_energy_cap", *a.PerCapacity)
			}
		}
	case model.RoleDemand:
		ess.set("parent", "demand")
		ess.set("carrier_in", t.CarrierIn.First())
		// The (negative) demand profile is a per-location file= resource; a
		// curtailable demand serves at most the profile instead of exactly.
		if t.DemandCurtailable {
			cons.set("force_resource", false)
		}
	case model.RoleConversion:
		if len(t.Flows) > 0 {
			if err := conversionFlows06(ess, cons, t.Flows, id); err != nil {
				return nil, err
			}
		} else {
			ess.set("parent", "conversion")
			ess.set("carrier_in", t.CarrierIn.First())
			ess.set("carrier_out", t.CarrierOut.First())
			if f, ok := emit.PerfEfficiency(t).Reduce(); ok {
				cons.set("energy_eff", f)
			}
		}
	case model.RoleStorage:
		ess.set("parent", "storage")
		car := t.CarrierOut.First()
		if car == "" {
			car = t.CarrierIn.First()
		}
		ess.set("carrier", car)
		if s := t.Storage; s != nil {
			if s.ChargeEff != nil || s.DischargeEff != nil {
				cons.set("energy_eff", storageEff06(s))
			}
			if s.SelfDischarge != nil {
				cons.set("storage_loss", *s.SelfDischarge)
			}
			if s.InitialSOC != nil {
				cons.set("storage_initial", *s.InitialSOC)
			}
			if s.MaxHours != nil && t.Capacity != nil && t.Capacity.Max != nil {
				cons.set("storage_cap_max", *t.Capacity.Max**s.MaxHours)
			}
			if ec := s.EnergyCapacity; ec != nil {
				if ec.Max != nil {
					cons.set("storage_cap_max", *ec.Max)
				}
				if ec.Min != nil {
					cons.set("storage_cap_min", *ec.Min)
				}
				if !ec.Expandable && ec.Existing > 0 {
					cons.set("storage_cap_equals", ec.Existing)
				}
			}
			if s.DepthOfDischarge != nil {
				cons.set("storage_discharge_depth", *s.DepthOfDischarge)
			}
			if s.NoSimultaneousChargeDischarge {
				cons.set("force_asynchronous_prod_con", true)
			}
			if r := storageRate(s); r != nil {
				cons.set("energy_cap_per_storage_cap_max", *r)
			}
			if s.Cyclic {
				st.cyclic++
			} else {
				st.nonCyclic++
			}
		}
	}

	// Capacity: continuous bounds, or integer units for a committable tech
	// (energy_cap_per_unit sizes the units; the bounds are the unit counts).
	if t.Operation == nil || !t.Operation.Committable {
		capacity06(cons, t.Capacity)
	} else {
		unit, _ := unitSize(t.Capacity)
		cons.set("energy_cap_per_unit", unit)
		maxUnits := 1.0
		if t.Capacity != nil && t.Capacity.Max != nil && unit > 0 {
			maxUnits = math.Ceil(*t.Capacity.Max/unit - 1e-9)
		}
		if t.Capacity != nil && t.Capacity.UnitsMax != nil {
			maxUnits = float64(*t.Capacity.UnitsMax)
		}
		cons.set("units_max", int(maxUnits))
		if t.Capacity != nil && t.Capacity.UnitsMin != nil {
			cons.set("units_min", *t.Capacity.UnitsMin)
		}
	}
	if t.Capacity != nil && t.Capacity.SystemwideMin != nil {
		g := newYAML()
		g.set("techs", []any{id})
		g.set("energy_cap_min", *t.Capacity.SystemwideMin)
		st.groups.set(id+"_systemwide_min", g)
	}
	if t.AnnualOutputMin != nil {
		g := newYAML()
		g.set("techs", []any{id})
		g.set("carrier_prod_min", classValue06(t.CarrierOut.First(), *t.AnnualOutputMin))
		st.groups.set(id+"_annual_output_min", g)
	}
	if t.AnnualOutputMax != nil {
		g := newYAML()
		g.set("techs", []any{id})
		g.set("carrier_prod_max", classValue06(t.CarrierOut.First(), *t.AnnualOutputMax))
		st.groups.set(id+"_annual_output_max", g)
	}
	operation06(cons, t.Operation)
	fixed, emis := costs06(costs, t, m, st)
	if emis {
		st.anyEmis = true
	}
	scored := st.spores && t.Role != model.RoleDemand
	if scored {
		costs.set(v06SporesClass, sporesScoreCost06())
	}
	// 0.6 requires a lifetime alongside any fixed cost (the SPORES score is
	// one); otherwise the component lifetime is informational only.
	if fixed || scored {
		cons.set("lifetime", lifetime06(t))
	} else if t.Lifetime != nil {
		cons.set("lifetime", *t.Lifetime)
	}

	n := newYAML()
	n.set("essentials", ess)
	if cons.len() > 0 {
		n.set("constraints", cons)
	}
	if costs.len() > 0 {
		n.set("costs", costs)
	}
	if !t.IsActive() {
		n.set("exists", false)
	}
	if err := mergeNativeDeepIntoYAML(n, t.Native.For(model.TargetCalliope06)); err != nil {
		return nil, fmt.Errorf("tech %q: %w", id, err)
	}
	return n, nil
}

// sporesScoreCost06 is the per-tech spores_score cost the 0.6 SPORES recipe
// expects on every capacity-bearing tech (the algorithm rewrites energy_cap
// between iterations).
func sporesScoreCost06() *yamlNode {
	c := newYAML()
	c.set("energy_cap", 0)
	c.set("interest_rate", 1)
	return c
}

// availability06 picks the per-timestep availability of a supply tech:
// operation.equals_pu (fixed dispatch, force_resource), else
// operation.max_pu, else a physics model's precomputed profile.
func availability06(t model.Technology) (*model.Value, bool) {
	if t.Operation != nil && t.Operation.EqualsPU != nil {
		return t.Operation.EqualsPU, true
	}
	if t.Operation != nil && t.Operation.MaxPU != nil {
		return t.Operation.MaxPU, false
	}
	if t.Performance != nil && t.Performance.Kind() == model.PerfPhysics {
		return t.Performance.Precomputed, false
	}
	return nil, false
}

// resourceUnit06 maps the canonical source unit onto 0.6's resource_unit.
func resourceUnit06(u string) string {
	switch u {
	case "per_area":
		return "energy_per_area"
	case "per_cap":
		return "energy_per_cap"
	default:
		return "energy"
	}
}

// storageEff06 is the single energy_eff 0.6 applies to both charging and
// discharging: sqrt(charge * discharge) preserves the round-trip efficiency
// when the two differ (ValidateJob warns).
func storageEff06(s *model.Storage) float64 {
	switch {
	case s.ChargeEff != nil && s.DischargeEff != nil:
		if *s.ChargeEff == *s.DischargeEff {
			return *s.ChargeEff
		}
		return math.Sqrt(*s.ChargeEff * *s.DischargeEff)
	case s.ChargeEff != nil:
		return *s.ChargeEff
	case s.DischargeEff != nil:
		return *s.DischargeEff
	}
	return 1
}

// conversionFlows06 renders a multi-port conversion. One input and one output
// is a plain conversion tech; more outputs become conversion_plus with the
// first output as the primary carrier: energy_eff is the primary output per
// unit of input, and every further output tier's carrier_ratio is relative to
// the primary output (0.6 semantics), i.e. ratio / primary ratio.
// ValidateJob guarantees a single input and at most three outputs.
func conversionFlows06(ess, cons *yamlNode, flows []model.Flow, id string) error {
	var in *model.Flow
	var outs []model.Flow
	for i := range flows {
		f := flows[i]
		if f.Direction == model.FlowIn {
			if in == nil || f.Reference {
				in = &flows[i]
			}
		} else {
			outs = append(outs, f)
		}
	}
	if in == nil || len(outs) == 0 {
		return fmt.Errorf("tech %q: flows need one input and at least one output", id)
	}
	ess.set("carrier_in", in.Carrier)
	if len(outs) == 1 {
		ess.set("parent", "conversion")
		ess.set("carrier_out", outs[0].Carrier)
		cons.set("energy_eff", outs[0].Ratio)
		return nil
	}
	ess.set("parent", "conversion_plus")
	primary := outs[0]
	if primary.Ratio <= 0 {
		return fmt.Errorf("tech %q: the primary output flow (%s) needs a positive ratio", id, primary.Carrier)
	}
	ess.set("carrier_out", primary.Carrier)
	ratios := newYAML()
	for i, o := range outs[1:] {
		tier := fmt.Sprintf("carrier_out_%d", i+2)
		ess.set(tier, o.Carrier)
		r := newYAML()
		r.set(o.Carrier, o.Ratio/primary.Ratio)
		ratios.set(tier, r)
	}
	ess.set("primary_carrier_out", primary.Carrier)
	cons.set("energy_eff", primary.Ratio)
	cons.set("carrier_ratios", ratios)
	return nil
}

// capacity06 renders the continuous capacity bounds: expandable techs get
// energy_cap_max/min, existing capacity is fixed with energy_cap_equals (or
// capped with energy_cap_max when it may be retired continuously).
func capacity06(cons *yamlNode, c *model.Capacity) {
	if c == nil {
		return
	}
	if c.Expandable {
		if max := c.EffectiveMax(); max != nil {
			cons.set("energy_cap_max", *max)
		}
		if min := c.EffectiveMin(); min != nil {
			cons.set("energy_cap_min", *min)
		}
	} else if c.Existing > 0 {
		if c.DecommissionMode() == "continuous" {
			cons.set("energy_cap_max", c.Existing)
		} else {
			cons.set("energy_cap_equals", c.Existing)
		}
	}
	if c.SystemwideMax != nil {
		cons.set("energy_cap_max_systemwide", *c.SystemwideMax)
	}
}

// operation06 emits the dispatch envelope 0.6 supports directly: the minimum
// stable load (energy_cap_min_use) and the symmetric ramp rate.
func operation06(cons *yamlNode, op *model.Operation) {
	if op == nil {
		return
	}
	if v, ok := op.MinPU.Reduce(); ok {
		cons.set("energy_cap_min_use", v)
	}
	if f, ok := op.RampUp.Reduce(); ok {
		cons.set("energy_ramping", f)
	} else if f, ok := op.RampDown.Reduce(); ok {
		cons.set("energy_ramping", f)
	}
}

// costs06 renders the per-class costs and the emissions class (a tech
// emission factor on production, om_prod, and the consumed carrier's co2
// intensity on consumption, om_con). Overnight investment passes through:
// 0.6 annualizes it via interest_rate + lifetime, and REQUIRES both on every
// class that carries a fixed cost (energy_cap, storage_cap, purchase), so
// each such class gets an interest_rate: the effective rate for an overnight
// basis, 0 for an already-annualized one (with lifetime 1, see tech06, the
// figure is then charged once per year unchanged). Returns (fixedCost,
// anyEmissions).
func costs06(costs *yamlNode, t model.Technology, m *model.Model, st *state06) (fixed, emissions bool) {
	for _, class := range emit.Keys(t.Costs) {
		c := t.Costs[class]
		cc := newYAML()
		classFixed := false
		if c.InvestmentPerCapacity != nil {
			cc.set("energy_cap", *c.InvestmentPerCapacity)
			classFixed = true
		}
		if c.FixedOM != nil {
			cc.set("om_annual", *c.FixedOM)
		}
		if c.FixedOMFraction != nil {
			cc.set("om_annual_investment_fraction", *c.FixedOMFraction)
			classFixed = true
		}
		if c.InvestmentPerEnergyCapacity != nil {
			cc.set("storage_cap", *c.InvestmentPerEnergyCapacity)
			classFixed = true
		}
		if c.Purchase != nil {
			cc.set("purchase", *c.Purchase)
			classFixed = true
		}
		if v, ok := c.VariableOM.Reduce(); ok {
			cc.set("om_prod", v)
		}
		if v, ok := c.FuelCost.Reduce(); ok {
			cc.set("om_con", v)
		}
		if classFixed {
			cc.set("interest_rate", interestRate06(t, m))
			fixed = true
		}
		if cc.len() > 0 {
			costs.set(class, cc)
		}
	}

	em := newYAML()
	if t.EmissionFactor != nil && *t.EmissionFactor != 0 {
		em.set("om_prod", *t.EmissionFactor)
	}
	intensity, found := 0.0, false
	consume := func(c string) {
		if c == "" || found {
			return
		}
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
		em.set("om_con", intensity)
	}
	if em.len() == 0 {
		return fixed, false
	}
	costs.set(v06EmissionClass, em)
	return fixed, true
}

// interestRate06 is the rate 0.6 annualizes a tech's fixed costs with: the
// effective (global or component) rate for an overnight basis, else 0.
func interestRate06(t model.Technology, m *model.Model) float64 {
	if t.CostBasis == model.CostAnnualized {
		return 0
	}
	if rate := m.EffectiveRate(t); rate != nil {
		return *rate
	}
	return 0
}

// lifetime06 is the lifetime 0.6 annualizes a tech's fixed costs over: the
// component lifetime for an overnight basis, else 1 (an annualized figure, or
// an overnight one without a lifetime, is charged in full every year — the
// same outcome as Calliope 0.7's default depreciation rate of 1).
func lifetime06(t model.Technology) float64 {
	if t.CostBasis != model.CostAnnualized && t.Lifetime != nil && *t.Lifetime > 0 {
		return *t.Lifetime
	}
	return 1
}

// operateCap06 pins an element's size for operate mode as energy_cap_equals.
// Calliope 0.6 documents a fallback from energy_cap_max to a fixed size in
// operate mode, but it does not reach every tech — a market import with only
// energy_cap_max ran unbounded (issue #8) — so every sized element gets the
// explicit value.
func operateCap06(n *yamlNode, v float64) {
	cons, ok := n.vals["constraints"].(*yamlNode)
	if !ok {
		cons = newYAML()
		n.set("constraints", cons)
	}
	cons.set("energy_cap_equals", v)
}

// applyOperateCaps06 fixes every capacity for operate mode: 0.6 falls back to
// energy_cap_max when energy_cap_equals is absent, but an explicit equals is
// unambiguous (ValidateJob guarantees a resolvable capacity).
func applyOperateCaps06(n *yamlNode, t model.Technology) {
	if t.Role == model.RoleDemand {
		return
	}
	cons, ok := n.vals["constraints"].(*yamlNode)
	if !ok {
		cons = newYAML()
		n.set("constraints", cons)
	}
	if v, ok := fixedCap(t.Capacity); ok && cons.vals["energy_cap_per_unit"] == nil {
		cons.set("energy_cap_equals", v)
	}
	if t.Role == model.RoleStorage && t.Storage != nil {
		if v, ok := fixedCap(t.Storage.EnergyCapacity); ok {
			cons.set("storage_cap_equals", v)
		}
	}
}

// locationTech06 returns the per-location entry for a tech: the file=
// references for its series-valued resource at this node (demand profiles,
// negated; supply availability or source series) and the capacity/cost
// overrides from node_overrides. nil means "placed here, nothing specific".
func locationTech06(t model.Technology, node, id string, m *model.Model, st *state06) *yamlNode {
	e := t.At(node)
	cons, costs := newYAML(), newYAML()
	n := emit.SnapshotCount(m)

	switch e.Role {
	case model.RoleDemand:
		file := id + "__resource.csv"
		vals := make([]float64, n)
		if v, ok := e.DemandProfile.Reduce(); ok {
			for i := range vals {
				vals[i] = -v
			}
		} else if e.DemandProfile.IsSeries() {
			series, _ := emit.SeriesValues(m, e.DemandProfile.SeriesID())
			for i := range vals {
				vals[i] = -emit.SeriesAt(series, i)
			}
		}
		st.column(file, node, vals)
		cons.set("resource", fileRef06(file, node))
	case model.RoleSupply:
		var series *model.Value
		if s := e.Source; s != nil && s.Max.IsSeries() {
			series = s.Max
		} else if av, _ := availability06(e); av.IsSeries() {
			series = av
		}
		if series != nil {
			file := id + "__resource.csv"
			vals, _ := emit.SeriesValues(m, series.SeriesID())
			padded := make([]float64, n)
			for i := range padded {
				padded[i] = emit.SeriesAt(vals, i)
			}
			st.column(file, node, padded)
			cons.set("resource", fileRef06(file, node))
		}
	}

	if ov, ok := t.NodeOverrides[node]; ok {
		capacity06(cons, ov.Capacity)
		for _, class := range emit.Keys(ov.Costs) {
			if c := ov.Costs[class]; c.InvestmentPerCapacity != nil {
				cc := newYAML()
				cc.set("energy_cap", *c.InvestmentPerCapacity)
				cc.set("interest_rate", interestRate06(e, m))
				costs.set(class, cc)
			}
		}
	}

	out := newYAML()
	if cons.len() > 0 {
		out.set("constraints", cons)
	}
	if costs.len() > 0 {
		out.set("costs", costs)
	}
	if out.len() == 0 {
		return nil
	}
	return out
}

// fileRef06 is 0.6's timeseries reference: the file (under
// model.timeseries_data_path) and the column to read.
func fileRef06(file, column string) string {
	return "file=" + file + ":" + column
}

// transmission06 renders a transmission arc as a transmission tech plus its
// links entry (where the distance lives).
func transmission06(l model.Transmission, id string, st *state06) (tech, link *yamlNode, err error) {
	ess, cons, costs := newYAML(), newYAML(), newYAML()
	ess.set("name", id)
	ess.set("parent", "transmission")
	ess.set("carrier", l.Carrier)
	if f, ok := l.Efficiency.Reduce(); ok {
		cons.set("energy_eff", f)
	}
	if !l.Bidirectional {
		cons.set("one_way", true)
	}
	capacity06(cons, l.Capacity)
	// Per-distance loss composes multiplicatively (eff_per_distance^distance),
	// the standard small-loss approximation of the canonical linear loss.
	if l.LossPerDistance != nil {
		cons.set("energy_eff_per_distance", 1-*l.LossPerDistance)
	}
	if l.MinFlow != nil {
		cons.set("energy_cap_min_use", *l.MinFlow)
	}
	// A transmission arc carries no lifetime/interest in the canonical model,
	// so its per-distance investment is charged in full every year (lifetime
	// 1, rate 0 — what Calliope 0.7's default depreciation rate of 1 does).
	fixed := false
	for _, class := range emit.Keys(l.Costs) {
		if c := l.Costs[class]; c.InvestmentPerCapacityDistance != nil {
			cc := newYAML()
			cc.set("energy_cap_per_distance", *c.InvestmentPerCapacityDistance)
			cc.set("interest_rate", 0)
			costs.set(class, cc)
			fixed = true
		}
	}
	if fixed || st.spores {
		cons.set("lifetime", 1)
	}
	if l.EmissionFactor != nil && *l.EmissionFactor != 0 {
		em := newYAML()
		em.set("om_prod", *l.EmissionFactor)
		costs.set(v06EmissionClass, em)
		st.anyEmis = true
	}
	if st.spores {
		costs.set(v06SporesClass, sporesScoreCost06())
	}
	tech = newYAML()
	tech.set("essentials", ess)
	if cons.len() > 0 {
		tech.set("constraints", cons)
	}
	if costs.len() > 0 {
		tech.set("costs", costs)
	}
	if !l.IsActive() {
		tech.set("exists", false)
	}
	if err := mergeNativeDeepIntoYAML(tech, l.Native.For(model.TargetCalliope06)); err != nil {
		return nil, nil, fmt.Errorf("transmission %q: %w", id, err)
	}
	link = newYAML()
	if l.Distance != nil {
		link.set("distance", *l.Distance)
	}
	return tech, link, nil
}

// tradeImport06 renders an import market as a supply tech selling the carrier
// at the import price, capped at the import limit; the emissions class carries
// the import emission factor.
func tradeImport06(tr model.Trade, id string, st *state06) (*yamlNode, error) {
	ess, cons, costs := newYAML(), newYAML(), newYAML()
	ess.set("name", id+" import")
	ess.set("parent", "supply")
	ess.set("carrier_out", tr.Carrier)
	if f, ok := tr.Import.Limit.Reduce(); ok {
		cons.set("energy_cap_max", f)
	}
	if f, ok := tr.Import.Price.Reduce(); ok && f != 0 {
		cc := newYAML()
		cc.set("om_prod", f)
		costs.set(model.PrimaryCostClass, cc)
	}
	if ef := tr.Import.EmissionFactor; ef != nil && *ef != 0 {
		em := newYAML()
		em.set("om_prod", *ef)
		costs.set(v06EmissionClass, em)
		st.anyEmis = true
	}
	if st.spores {
		costs.set(v06SporesClass, sporesScoreCost06())
	}
	return assembleTech06(ess, cons, costs, tr.Native.For(model.TargetCalliope06), "trade "+id)
}

// tradeExport06 renders an export market as a flexible demand tech: its
// (negative) resource is the export limit, not forced, and it is PAID the
// export price (negative om_con = revenue). The emissions class carries the
// export emission factor.
func tradeExport06(tr model.Trade, id string, st *state06) (*yamlNode, error) {
	ess, cons, costs := newYAML(), newYAML(), newYAML()
	ess.set("name", id+" export")
	ess.set("parent", "demand")
	ess.set("carrier_in", tr.Carrier)
	limit, _ := tr.Export.Limit.Reduce() // ValidateJob guarantees a scalar limit
	cons.set("resource", -limit)
	cons.set("force_resource", false)
	if f, ok := tr.Export.Price.Reduce(); ok && f != 0 {
		cc := newYAML()
		cc.set("om_con", -f)
		costs.set(model.PrimaryCostClass, cc)
	}
	if ef := tr.Export.EmissionFactor; ef != nil && *ef != 0 {
		em := newYAML()
		em.set("om_con", *ef)
		costs.set(v06EmissionClass, em)
		st.anyEmis = true
	}
	return assembleTech06(ess, cons, costs, tr.Native.For(model.TargetCalliope06), "trade "+id)
}

func assembleTech06(ess, cons, costs *yamlNode, native []byte, what string) (*yamlNode, error) {
	n := newYAML()
	n.set("essentials", ess)
	if cons.len() > 0 {
		n.set("constraints", cons)
	}
	if costs.len() > 0 {
		n.set("costs", costs)
	}
	if err := mergeNativeDeepIntoYAML(n, native); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return n, nil
}

// --- constraints -> group_constraints -----------------------------------------

// senseSuffix06 maps a constraint sense onto 0.6's _max/_min/_equals suffix.
func senseSuffix06(sense string) string {
	switch sense {
	case ">=":
		return "min"
	case "==":
		return "equals"
	default:
		return "max"
	}
}

// classValue06 builds the {class: value} mapping group constraints take for
// cost- and carrier-indexed bounds.
func classValue06(class string, v float64) *yamlNode {
	n := newYAML()
	n.set(class, v)
	return n
}

func constraintName06(c model.Constraint, i int) string {
	if c.Name != "" {
		return c.Name
	}
	return fmt.Sprintf("constraint_%d", i+1)
}

// groupConstraint06 translates one portable constraint into a 0.6 group
// constraint. Group constraints bound ONE quantity (capacity, production,
// consumption, storage capacity or a cost class) summed over a tech/location
// set, so only single-term constraints map; the coefficient is folded into
// the bound (flipping the sense when negative). Shared by ValidateJob (to
// reject the rest with a pointed message) and the emitter.
func groupConstraint06(c model.Constraint, i int, m *model.Model) (*yamlNode, error) {
	name := constraintName06(c, i)
	if len(c.Terms) != 1 {
		return nil, fmt.Errorf("constraint %q: calliope-0.6 group_constraints bound a single quantity over a tech/node set, but the constraint has %d terms; use target calliope (custom math) for multi-term constraints", name, len(c.Terms))
	}
	t := c.Terms[0]
	if t.Coefficient == 0 {
		return nil, fmt.Errorf("constraint %q: the term coefficient must not be zero", name)
	}
	bound := c.Bound / t.Coefficient
	sense := c.Sense
	if t.Coefficient < 0 {
		switch sense {
		case "<=":
			sense = ">="
		case ">=":
			sense = "<="
		}
	}
	suffix := senseSuffix06(sense)
	g := newYAML()
	if len(t.Techs) > 0 {
		g.set("techs", toAny(t.Techs))
	}
	if len(t.Nodes) > 0 {
		g.set("locs", toAny(t.Nodes))
	}
	switch t.Variable {
	case model.VarCapacity:
		g.set("energy_cap_"+suffix, bound)
	case model.VarStorageCap:
		g.set("storage_cap_"+suffix, bound)
	case model.VarEmissions:
		g.set("cost_"+suffix, classValue06(v06EmissionClass, bound))
	case model.VarFlowOut:
		carrier, err := termCarrier06(name, t, m, true)
		if err != nil {
			return nil, err
		}
		g.set("carrier_prod_"+suffix, classValue06(carrier, bound))
	case model.VarFlowIn:
		carrier, err := termCarrier06(name, t, m, false)
		if err != nil {
			return nil, err
		}
		// carrier_con is negative in 0.6: a cap on consumption |con| <= b is
		// con >= -b (carrier_con_min), a floor is con <= -b (carrier_con_max).
		flipped := map[string]string{"max": "min", "min": "max", "equals": "equals"}[suffix]
		g.set("carrier_con_"+flipped, classValue06(carrier, -bound))
	default:
		return nil, fmt.Errorf("constraint %q: variable %q has no calliope-0.6 group constraint", name, t.Variable)
	}
	return g, nil
}

// termCarrier06 resolves the single carrier a production/consumption group
// constraint is indexed by: the term's one carrier, else the common carrier of
// the scoped techs.
func termCarrier06(name string, t model.Term, m *model.Model, out bool) (string, error) {
	if len(t.Carriers) == 1 {
		return t.Carriers[0], nil
	}
	if len(t.Carriers) > 1 {
		return "", fmt.Errorf("constraint %q: calliope-0.6 carrier_prod/carrier_con group constraints take one carrier; got %v", name, t.Carriers)
	}
	carrier := ""
	for _, id := range t.Techs {
		tech, ok := m.Technologies[id]
		if !ok {
			continue
		}
		c := tech.CarrierOut.First()
		if !out {
			c = tech.CarrierIn.First()
		}
		if carrier != "" && c != carrier {
			return "", fmt.Errorf("constraint %q: the scoped techs use different carriers (%s, %s); name the carrier in the term", name, carrier, c)
		}
		carrier = c
	}
	if carrier == "" {
		return "", fmt.Errorf("constraint %q: a %s term on calliope-0.6 needs a carrier (set term.carriers)", name, t.Variable)
	}
	return carrier, nil
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// writeCSV06 materializes one timeseries_data/ file: a datetime index column
// (blank header) and one column per node, over the model's snapshot calendar.
func writeCSV06(dir string, c *csv06, m *model.Model) error {
	n := emit.SnapshotCount(m)
	labels := emit.SnapshotLabels(m.Time, n)
	header := append([]string{""}, c.nodes...)
	rows := make([][]string, 0, n+1)
	rows = append(rows, header)
	for i := 0; i < n; i++ {
		row := make([]string, 0, len(c.nodes)+1)
		row = append(row, labels[i])
		for _, node := range c.nodes {
			row = append(row, emit.Ftoa(emit.SeriesAt(c.columns[node], i)))
		}
		rows = append(rows, row)
	}
	return emit.WriteCSV(dir, c.file, rows)
}
