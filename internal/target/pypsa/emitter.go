// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

// Emitter writes a PyPSA CSV import folder (loadable with
// n.import_from_csv_folder). It fuses (node, carrier) into buses, annualizes
// overnight capex into per-year capital_cost, expands multi-port conversion
// into multi-output Links, emits unit-commitment/storage detail as columns,
// turns Trade into market generators, renders EmissionLimits as global
// constraints and systemwide capacity bounds as sidecar constraints, and
// merges Native.pypsa blocks as extra columns.
//
// Static parameters are written inline; series-valued parameters are
// materialized into per-attribute snapshot CSVs by MaterializeTimeSeries
// (materialize.go), which the PyPSA target invokes right after Emit.
type Emitter struct{}

// pypsaTargetVersion is stamped into network.csv so PyPSA knows which version
// wrote the input folder (no stamp = a "v0.0.0" import warning). Keep in sync
// with the pin in environment/requirements.txt.
const pypsaTargetVersion = "1.2.4"

// Emit writes the CSV import folder and its sidecars under outDir and returns
// outDir as the entrypoint. Time-varying attributes are materialized
// separately by MaterializeTimeSeries.
func (Emitter) Emit(j *model.Job, outDir string) (string, error) {
	m := &j.Model
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}

	// network.csv: network-level attributes (name + writing version).
	name := m.Metadata.Name
	if name == "" {
		name = "energymodel"
	}
	if err := emit.WriteCSV(outDir, "network.csv", [][]string{
		{"name", "pypsa_version"},
		{name, pypsaTargetVersion},
	}); err != nil {
		return "", err
	}

	// buses (one per node,carrier) + carriers ---------------------------------
	buses := &pcsv{base: []string{"name", "carrier", "x", "y"}}
	grid := busGrid(m)
	pairs := make([][2]string, 0, len(grid))
	for pair := range grid {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i][0] != pairs[j][0] {
			return pairs[i][0] < pairs[j][0]
		}
		return pairs[i][1] < pairs[j][1]
	})
	for _, pair := range pairs {
		node, carrier := pair[0], pair[1]
		row := map[string]string{"name": BusID(node, carrier), "carrier": carrier}
		if n, ok := m.Nodes[node]; ok {
			if n.Coords != nil {
				row["x"] = emit.Ftoa(n.Coords.Lon)
				row["y"] = emit.Ftoa(n.Coords.Lat)
			}
			if err := mergeNativeInto(row, n.Native.For(model.TargetPyPSA)); err != nil {
				return "", fmt.Errorf("node %q: %w", node, err)
			}
		}
		buses.add(row)
	}
	if err := buses.write(outDir, "buses.csv"); err != nil {
		return "", err
	}

	carriers := &pcsv{base: []string{"name", "co2_emissions"}}
	for _, id := range emit.Keys(m.Carriers) {
		c := m.Carriers[id]
		row := map[string]string{"name": id, "co2_emissions": "0"}
		if c.CO2Intensity != nil {
			row["co2_emissions"] = emit.Ftoa(*c.CO2Intensity)
		}
		if err := mergeNativeInto(row, c.Native.For(model.TargetPyPSA)); err != nil {
			return "", fmt.Errorf("carrier %q: %w", id, err)
		}
		carriers.add(row)
	}
	if err := carriers.write(outDir, "carriers.csv"); err != nil {
		return "", err
	}

	// component tables --------------------------------------------------------
	gens := &pcsv{base: []string{"name", "bus", "carrier", "p_nom", "p_nom_extendable", "p_nom_max", "capital_cost", "marginal_cost", "efficiency"}}
	loads := &pcsv{base: []string{"name", "bus", "p_set"}}
	links := &pcsv{base: []string{"name", "bus0", "bus1", "p_nom", "p_nom_extendable", "p_nom_max", "efficiency", "capital_cost"}}
	sus := &pcsv{base: []string{"name", "bus", "carrier", "p_nom", "p_nom_extendable", "max_hours", "efficiency_store", "efficiency_dispatch", "capital_cost"}}

	var perfSpecs []model.PerfEntry
	for _, id := range emit.Keys(m.Technologies) {
		t := m.Technologies[id]
		for _, node := range t.Node {
			name := id
			if len(t.Node) > 1 {
				name = id + "@" + node
			}
			eff := t.At(node) // apply per-node overrides
			if eff.Performance != nil && eff.Performance.Kind() != model.PerfConstant {
				perfSpecs = append(perfSpecs, model.PerfEntry{Tech: name, Node: node, Performance: *eff.Performance})
			}
			var row map[string]string
			var tbl *pcsv
			switch eff.Role {
			case model.RoleSupply:
				tbl = gens
				row = map[string]string{
					"name": name, "bus": BusID(node, eff.CarrierOut.First()), "carrier": eff.CarrierOut.First(),
					"p_nom": capNom(eff.Capacity), "p_nom_extendable": capExt(eff.Capacity), "p_nom_max": capMax(eff.Capacity),
					"capital_cost": emit.Ftoa(pypsaCapitalCost(m, eff)), "marginal_cost": marginal(eff), "efficiency": effOr(eff, 1),
				}
				if eff.AnnualOutputMin != nil {
					row["e_sum_min"] = emit.Ftoa(*eff.AnnualOutputMin)
				}
				if eff.AnnualOutputMax != nil {
					row["e_sum_max"] = emit.Ftoa(*eff.AnnualOutputMax)
				}
				applyOperation(row, eff.Operation)
			case model.RoleDemand:
				tbl = loads
				row = map[string]string{"name": name, "bus": BusID(node, eff.CarrierIn.First()), "p_set": emit.ScalarOr(eff.DemandProfile, 0)}
			case model.RoleConversion:
				tbl = links
				if len(eff.Flows) > 0 {
					var err error
					if row, err = pypsaMultiLink(m, name, node, eff); err != nil {
						return "", fmt.Errorf("tech %q: %w", id, err)
					}
				} else {
					row = map[string]string{
						"name": name, "bus0": BusID(node, eff.CarrierIn.First()), "bus1": BusID(node, eff.CarrierOut.First()),
						"p_nom": capNom(eff.Capacity), "p_nom_extendable": capExt(eff.Capacity), "p_nom_max": capMax(eff.Capacity),
						"efficiency": effOr(eff, 1), "capital_cost": emit.Ftoa(pypsaCapitalCost(m, eff)), "marginal_cost": marginal(eff),
					}
				}
				applyOperation(row, eff.Operation)
			case model.RoleStorage:
				tbl = sus
				carrier := eff.CarrierOut.First()
				if carrier == "" {
					carrier = eff.CarrierIn.First()
				}
				row = map[string]string{
					"name": name, "bus": BusID(node, carrier), "carrier": carrier,
					"p_nom": capNom(eff.Capacity), "p_nom_extendable": capExt(eff.Capacity), "max_hours": maxHours(eff),
					"efficiency_store": effp(eff.Storage, chargeEff), "efficiency_dispatch": effp(eff.Storage, dischargeEff),
					"capital_cost": emit.Ftoa(pypsaCapitalCost(m, eff)),
				}
				applyStorageDetail(row, eff.Storage)
			default:
				continue
			}
			setPNomMin(row, eff.Capacity)
			if eff.BuildYear != nil {
				row["build_year"] = strconv.Itoa(*eff.BuildYear)
			}
			if !eff.IsActive() {
				row["active"] = "False"
			}
			if err := mergeNativeInto(row, eff.Native.For(model.TargetPyPSA)); err != nil {
				return "", fmt.Errorf("tech %q: %w", id, err)
			}
			tbl.add(row)
		}
	}

	// transmission -> links across the node grid ------------------------------
	for _, id := range emit.Keys(m.Transmission) {
		l := m.Transmission[id]
		eff := 1.0
		if f, ok := l.Efficiency.Reduce(); ok {
			eff = f
		}
		// Distance-proportional losses fold into the link efficiency (PyPSA has
		// no per-km loss attribute). ValidateJob guarantees Distance is set and
		// the product stays below 1. Rounded to 12 significant decimals so the
		// CSV carries 0.9603 rather than float noise (0.96029999...).
		if l.LossPerDistance != nil && l.Distance != nil {
			eff = math.Round(eff*(1-*l.LossPerDistance**l.Distance)*1e12) / 1e12
		}
		row := map[string]string{
			"name": id, "bus0": BusID(l.From, l.Carrier), "bus1": BusID(l.To, l.Carrier),
			"p_nom": capNom(l.Capacity), "p_nom_extendable": capExt(l.Capacity), "p_nom_max": capMax(l.Capacity),
			"efficiency": emit.Ftoa(eff), "capital_cost": "0",
		}
		// PyPSA links are one-directional by default (p >= 0); a bidirectional
		// line may flow backwards down to -p_nom. A one-way link may instead
		// carry a minimum-flow floor.
		if l.Bidirectional {
			row["p_min_pu"] = "-1"
		} else if l.MinFlow != nil {
			row["p_min_pu"] = emit.Ftoa(*l.MinFlow)
		}
		// Auxiliary transport energy: a second input port withdrawing the
		// consumed carrier from the origin node per unit of flow (negative
		// efficiency, PyPSA's multi-port convention). ValidateJob rejects it on
		// bidirectional arcs, where reversed flow would inject the aux carrier.
		if ec := l.EnergyConsumption; ec != nil {
			rate := ec.PerFlow
			if l.Distance != nil {
				rate += ec.PerFlowDistance * *l.Distance
			}
			row["bus2"] = BusID(l.From, ec.Carrier)
			row["efficiency2"] = emit.Ftoa(-rate)
		}
		setPNomMin(row, l.Capacity)
		if !l.IsActive() {
			row["active"] = "False"
		}
		if err := mergeNativeInto(row, l.Native.For(model.TargetPyPSA)); err != nil {
			return "", fmt.Errorf("transmission %q: %w", id, err)
		}
		links.add(row)
	}

	// trade -> market generators ----------------------------------------------
	// Import: a generator that can inject up to `limit` at price `price`.
	// Export: a generator allowed to consume (p in [-limit, 0]). Its
	// marginal_cost is +price: the objective term marginal_cost*p then turns
	// negative (a revenue) exactly when energy is absorbed (p < 0).
	// A series-valued limit uses a fixed unit p_nom with the absolute limit as
	// a time-varying p_max_pu/p_min_pu (a plain bound multiplier, so values
	// above 1 are fine); MaterializeTimeSeries writes the per-snapshot CSVs.
	for _, id := range emit.Keys(m.Trade) {
		tr := m.Trade[id]
		bus := BusID(tr.Node, tr.Carrier)
		if tr.Import != nil {
			row := map[string]string{
				"name": id + "_import", "bus": bus, "carrier": tr.Carrier,
				"marginal_cost": emit.ValScalar(tr.Import.Price, 0), "efficiency": "1",
			}
			if tr.Import.Limit.IsSeries() {
				row["p_nom"], row["p_nom_extendable"] = "1", "False"
			} else {
				row["p_nom"], row["p_nom_extendable"] = "0", "True"
				row["p_nom_max"] = tradeLimit(tr.Import.Limit)
			}
			if err := mergeNativeInto(row, tr.Native.For(model.TargetPyPSA)); err != nil {
				return "", fmt.Errorf("trade %q: %w", id, err)
			}
			gens.add(row)
		}
		if tr.Export != nil {
			row := map[string]string{
				"name": id + "_export", "bus": bus, "carrier": tr.Carrier,
				"marginal_cost": emit.ValScalar(tr.Export.Price, 0), "efficiency": "1",
				"p_max_pu": "0",
			}
			if tr.Export.Limit.IsSeries() {
				row["p_nom"], row["p_nom_extendable"] = "1", "False"
			} else {
				row["p_nom"], row["p_nom_extendable"] = "0", "True"
				row["p_nom_max"] = tradeLimit(tr.Export.Limit)
				row["p_min_pu"] = "-1"
			}
			if err := mergeNativeInto(row, tr.Native.For(model.TargetPyPSA)); err != nil {
				return "", fmt.Errorf("trade %q: %w", id, err)
			}
			gens.add(row)
		}
	}

	for file, tbl := range map[string]*pcsv{
		"generators.csv": gens, "loads.csv": loads, "links.csv": links, "storage_units.csv": sus,
	} {
		if err := tbl.write(outDir, file); err != nil {
			return "", err
		}
	}

	// emission limits + linear constraints -> global constraints / sidecar ------
	gc := &pcsv{base: []string{"name", "type", "carrier_attribute", "sense", "constant"}}
	for i, el := range m.EmissionLimits {
		name := el.Name
		if name == "" {
			name = fmt.Sprintf("emission_limit_%d", i+1)
		}
		// PyPSA's carrier attribute is "co2_emissions"; the canonical emission
		// carrier ("co2", or unset) names the emission type, not the attribute.
		attr := el.Carrier
		if attr == "" || attr == "co2" {
			attr = "co2_emissions"
		}
		row := map[string]string{
			"name": name, "type": "primary_energy", "carrier_attribute": attr,
			"sense": el.Sense, "constant": emit.Ftoa(el.Limit),
		}
		if el.Period != "" {
			row["investment_period"] = el.Period
		}
		gc.add(row)
	}

	// Systemwide capacity bounds synthesize into portable sidecar constraints:
	// the capacity variable summed over every per-node instance of the
	// component (ValidateJob gates them out of the operate/stochastic modes,
	// which cannot carry sidecar constraints).
	var sidecar []model.Constraint
	sidecar = append(sidecar, systemwideConstraints(m)...)
	// Linear constraints: recognizable single-variable forms become PyPSA
	// GlobalConstraint rows; the rest go to a portable sidecar the orchestrator
	// applies via n.optimize(extra_functionality=...).
	for i, c := range m.Constraints {
		if gcType, attr, ok := globalType(c); ok {
			name := c.Name
			if name == "" {
				name = fmt.Sprintf("constraint_%d", i+1)
			}
			row := map[string]string{
				"name": name, "type": gcType, "carrier_attribute": attr,
				"sense": c.Sense, "constant": emit.Ftoa(c.Bound),
			}
			if c.Period != "" {
				row["investment_period"] = c.Period
			}
			gc.add(row)
		} else {
			sidecar = append(sidecar, c)
		}
	}
	if err := gc.write(outDir, "global_constraints.csv"); err != nil {
		return "", err
	}
	if len(sidecar) > 0 {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"constraints": sidecar}); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(outDir, "_constraints.json"), buf.Bytes(), 0o644); err != nil {
			return "", err
		}
	}

	// performance models -> sidecar for the orchestrator to precompute/linearize
	if len(perfSpecs) > 0 {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"performance": perfSpecs}); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(outDir, "_performance.json"), buf.Bytes(), 0o644); err != nil {
			return "", err
		}
	}

	// stochastic scenarios -> _scenarios.json sidecar (run.py sets these on the
	// scenario-expanded network after n.set_scenarios).
	if j.Experiment.EffectiveMode() == model.ModeStochastic {
		weights := NormalizedScenarioWeights(j.Experiment.Scenarios)
		type scenarioSpec struct {
			Name   string        `json:"name"`
			Weight float64       `json:"weight"`
			Sets   []ScenarioSet `json:"sets"`
		}
		specs := make([]scenarioSpec, 0, len(j.Experiment.Scenarios))
		for _, s := range j.Experiment.Scenarios {
			sets, err := ScenarioSets(m, s)
			if err != nil {
				return "", err
			}
			specs = append(specs, scenarioSpec{Name: s.Name, Weight: weights[s.Name], Sets: sets})
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"scenarios": specs}); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(outDir, "_scenarios.json"), buf.Bytes(), 0o644); err != nil {
			return "", err
		}
	}

	// system-wide native sidecar ----------------------------------------------
	if raw := m.Native.For(model.TargetPyPSA); len(raw) > 0 {
		if err := os.WriteFile(filepath.Join(outDir, "_native.pypsa.json"), raw, 0o644); err != nil {
			return "", err
		}
	}

	return outDir, nil
}

// pypsaMultiLink builds a multi-output Link row: bus0 is the reference input,
// each other flow becomes bus1/efficiency, bus2/efficiency2, ... with efficiency
// = ratio for outputs and -ratio for additional inputs (PyPSA's convention for
// withdrawing from a bus).
func pypsaMultiLink(m *model.Model, name, node string, t model.Technology) (map[string]string, error) {
	flows := t.Flows
	ref := -1
	for i, f := range flows {
		if f.Reference && f.Direction == model.FlowIn {
			ref = i
			break
		}
	}
	if ref == -1 {
		for i, f := range flows {
			if f.Direction == model.FlowIn {
				ref = i
				break
			}
		}
	}
	if ref == -1 {
		return nil, fmt.Errorf("multi-port conversion needs at least one input flow")
	}
	row := map[string]string{
		"name": name, "bus0": BusID(node, flows[ref].Carrier),
		"p_nom": capNom(t.Capacity), "p_nom_extendable": capExt(t.Capacity), "p_nom_max": capMax(t.Capacity),
		"capital_cost": emit.Ftoa(pypsaCapitalCost(m, t)), "marginal_cost": marginal(t),
	}
	port := 0
	for i, f := range flows {
		if i == ref {
			continue
		}
		port++
		eff := f.Ratio
		if f.Direction == model.FlowIn {
			eff = -f.Ratio
		}
		busCol := "bus" + strconv.Itoa(port)
		effCol := "efficiency"
		if port > 1 {
			effCol = "efficiency" + strconv.Itoa(port)
		}
		row[busCol] = BusID(node, f.Carrier)
		row[effCol] = emit.Ftoa(eff)
	}
	return row, nil
}

// applyOperation writes unit-commitment / ramping / availability columns.
func applyOperation(row map[string]string, op *model.Operation) {
	if op == nil {
		return
	}
	if op.Committable {
		row["committable"] = "True"
	}
	if op.StartUpCost != nil {
		row["start_up_cost"] = emit.Ftoa(*op.StartUpCost)
	}
	if op.ShutDownCost != nil {
		row["shut_down_cost"] = emit.Ftoa(*op.ShutDownCost)
	}
	if op.MinUptime != nil {
		row["min_up_time"] = emit.Ftoa(*op.MinUptime)
	}
	if op.MinDowntime != nil {
		row["min_down_time"] = emit.Ftoa(*op.MinDowntime)
	}
	if f, ok := op.RampUp.Reduce(); ok {
		row["ramp_limit_up"] = emit.Ftoa(f)
	}
	if f, ok := op.RampDown.Reduce(); ok {
		row["ramp_limit_down"] = emit.Ftoa(f)
	}
	if f, ok := op.MaxPU.Reduce(); ok {
		row["p_max_pu"] = emit.Ftoa(f)
	}
	if f, ok := op.MinPU.Reduce(); ok {
		row["p_min_pu"] = emit.Ftoa(f)
	}
	// A fixed dispatch pins both bounds (a non-curtailable resource); series
	// values take the same route through the materializer.
	if f, ok := op.EqualsPU.Reduce(); ok {
		row["p_max_pu"] = emit.Ftoa(f)
		row["p_min_pu"] = emit.Ftoa(f)
	}
}

// applyStorageDetail writes standing loss, initial/cyclic SoC, spill, inflow.
func applyStorageDetail(row map[string]string, s *model.Storage) {
	if s == nil {
		return
	}
	if s.SelfDischarge != nil {
		row["standing_loss"] = emit.Ftoa(*s.SelfDischarge)
	}
	if s.InitialSOC != nil {
		row["state_of_charge_initial"] = emit.Ftoa(*s.InitialSOC)
	}
	if s.Cyclic {
		row["cyclic_state_of_charge"] = "True"
	}
	if s.SpillCost != nil {
		row["spill_cost"] = emit.Ftoa(*s.SpillCost)
	}
	if f, ok := s.Inflow.Reduce(); ok {
		row["inflow"] = emit.Ftoa(f)
	}
}

// --- columnar CSV table with native/extra-driven columns -----------------

type pcsv struct {
	base []string
	rows []map[string]string
}

func (t *pcsv) add(r map[string]string) { t.rows = append(t.rows, r) }

func (t *pcsv) write(dir, file string) error {
	if len(t.rows) == 0 {
		return nil
	}
	extra := map[string]bool{}
	for _, r := range t.rows {
		for k := range r {
			if !inSlice(t.base, k) {
				extra[k] = true
			}
		}
	}
	extraCols := make([]string, 0, len(extra))
	for k := range extra {
		extraCols = append(extraCols, k)
	}
	sort.Strings(extraCols)
	cols := append(append([]string{}, t.base...), extraCols...)

	out := [][]string{cols}
	for _, r := range t.rows {
		line := make([]string, len(cols))
		for i, c := range cols {
			line[i] = r[c]
		}
		out = append(out, line)
	}
	return emit.WriteCSV(dir, file, out)
}

func inSlice(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// --- small value helpers -------------------------------------------------

func capNom(c *model.Capacity) string {
	if c == nil {
		return "0"
	}
	return emit.Ftoa(c.Existing)
}
func capExt(c *model.Capacity) string {
	if c == nil {
		return "False"
	}
	return emit.BoolPy(c.Expandable)
}
func capMax(c *model.Capacity) string {
	if max := c.EffectiveMax(); max != nil {
		return emit.Ftoa(*max)
	}
	return "inf"
}

// setPNomMin writes p_nom_min for an expandable component with a lower capacity
// bound — explicit or implied by units_min * per_unit (PyPSA's default is 0,
// so only a real minimum is emitted).
func setPNomMin(row map[string]string, c *model.Capacity) {
	if c != nil && c.Expandable {
		if min := c.EffectiveMin(); min != nil {
			row["p_nom_min"] = emit.Ftoa(*min)
		}
	}
}

// systemwideConstraints synthesizes sidecar constraints from capacity
// systemwide bounds: the capacity variable of every instance of the component
// (id and id@node) summed, bounded by the given constant.
func systemwideConstraints(m *model.Model) []model.Constraint {
	var out []model.Constraint
	add := func(id string, c *model.Capacity) {
		if c == nil {
			return
		}
		if c.SystemwideMax != nil {
			out = append(out, model.Constraint{
				Name:  id + "_systemwide_max",
				Terms: []model.Term{{Coefficient: 1, Variable: model.VarCapacity, Techs: []string{id}}},
				Sense: "<=", Bound: *c.SystemwideMax,
			})
		}
		if c.SystemwideMin != nil {
			out = append(out, model.Constraint{
				Name:  id + "_systemwide_min",
				Terms: []model.Term{{Coefficient: 1, Variable: model.VarCapacity, Techs: []string{id}}},
				Sense: ">=", Bound: *c.SystemwideMin,
			})
		}
	}
	for _, id := range emit.Keys(m.Technologies) {
		add(id, m.Technologies[id].Capacity)
	}
	for _, id := range emit.Keys(m.Transmission) {
		add(id, m.Transmission[id].Capacity)
	}
	return out
}

// tradeLimit renders a scalar trade limit (default: unbounded). Series limits
// take the fixed-p_nom route in the trade block above.
func tradeLimit(v *model.Value) string {
	if f, ok := v.Reduce(); ok {
		return emit.Ftoa(f)
	}
	return "inf"
}

func maxHours(t model.Technology) string {
	if t.Storage != nil && t.Storage.MaxHours != nil {
		return emit.Ftoa(*t.Storage.MaxHours)
	}
	return "1"
}

func effOr(t model.Technology, def float64) string {
	if f, ok := emit.PerfEfficiency(t).Reduce(); ok {
		return emit.Ftoa(f)
	}
	return emit.Ftoa(def)
}

// pypsaCapitalCost is the annualized investment plus annual fixed O&M. PyPSA has
// no separate fixed-cost column, so fixed_om (absolute) or fixed_om_fraction
// (fraction of the upfront investment) folds into capital_cost (both are
// per-capacity, per-year), keeping them generally-honored cost fields.
func pypsaCapitalCost(m *model.Model, t model.Technology) float64 {
	cc := annualizedInvestment(m, t, model.PrimaryCostClass)
	if c, ok := t.Costs[model.PrimaryCostClass]; ok {
		if c.FixedOM != nil {
			cc += *c.FixedOM
		}
		if c.FixedOMFraction != nil && c.InvestmentPerCapacity != nil {
			cc += *c.FixedOMFraction * *c.InvestmentPerCapacity
		}
	}
	return cc
}

// marginal is the per-MWh-output dispatch cost: variable O&M plus scalar fuel
// cost scaled to the output basis (fuel is priced per MWh INPUT, so it enters
// as fuel / efficiency). Series fuel costs are rejected by ValidateJob.
func marginal(t model.Technology) string {
	total, have := 0.0, false
	if c, ok := t.Costs[model.PrimaryCostClass]; ok {
		if f, ok2 := c.VariableOM.Reduce(); ok2 {
			total, have = f, true
		}
		if fuel, ok2 := c.FuelCost.Reduce(); ok2 {
			eff := 1.0
			if f, ok3 := emit.PerfEfficiency(t).Reduce(); ok3 && f > 0 {
				eff = f
			}
			total, have = total+fuel/eff, true
		}
	}
	if !have {
		return "0"
	}
	return emit.Ftoa(total)
}

type storageEff func(*model.Storage) *float64

func chargeEff(s *model.Storage) *float64    { return s.ChargeEff }
func dischargeEff(s *model.Storage) *float64 { return s.DischargeEff }

func effp(s *model.Storage, f storageEff) string {
	if s != nil {
		if v := f(s); v != nil {
			return emit.Ftoa(*v)
		}
	}
	return "1"
}
