// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa_test

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target"
	"github.com/enerplanet/meme/internal/target/pypsa"
)

// table reads an emitted CSV into rows keyed by column name, indexed by the
// first column (the component name or the snapshot label).
func table(t *testing.T, dir, file string) map[string]map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, file))
	if err != nil {
		t.Fatalf("open %s: %v", file, err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil || len(recs) == 0 {
		t.Fatalf("read %s: %v", file, err)
	}
	out := map[string]map[string]string{}
	for _, rec := range recs[1:] {
		row := map[string]string{}
		for i, col := range recs[0] {
			row[col] = rec[i]
		}
		out[rec[0]] = row
	}
	return out
}

func exists(dir, file string) bool {
	_, err := os.Stat(filepath.Join(dir, file))
	return err == nil
}

// emitPowerFlow validates and emits the power-flow scenario (optionally
// adapted) and returns the emit dir plus the validation warnings.
func emitPowerFlow(t *testing.T, mut func(j *model.Job)) (string, []string, model.Job) {
	t.Helper()
	j, err := scenarios.Load("pypsa_power_flow.json")
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(&j)
	}
	warns, err := target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	return dir, warns, j
}

// In mode power_flow the electrical carrier becomes a PyPSA AC network: buses
// with v_nom, a lines and a transformers table instead of links, generators
// with their control and fixed dispatch, series dispatch materialized.
func TestPowerFlowEmitsElectricalNetwork(t *testing.T) {
	dir, warns, _ := emitPowerFlow(t, nil)
	if len(warns) != 0 {
		t.Errorf("the reference scenario should validate without warnings, got %v", warns)
	}

	buses := table(t, dir, "buses.csv")
	for bus, vnom := range map[string]string{"hv::electricity": "110", "mv1::electricity": "20", "mv2::electricity": "20"} {
		if got := buses[bus]; got["carrier"] != "AC" || got["v_nom"] != vnom {
			t.Errorf("bus %s: want carrier AC v_nom %s, got %v", bus, vnom, got)
		}
	}
	if buses["hv::electricity"]["x"] != "11" || buses["hv::electricity"]["y"] != "48" {
		t.Errorf("bus coordinates must survive: %v", buses["hv::electricity"])
	}
	carriers := table(t, dir, "carriers.csv")
	if _, ok := carriers["AC"]; !ok {
		t.Errorf("carriers.csv must declare AC: %v", carriers)
	}
	if _, ok := carriers["electricity"]; !ok {
		t.Errorf("the canonical carrier stays declared for the components: %v", carriers)
	}

	lines := table(t, dir, "lines.csv")
	want := map[string]string{"bus0": "mv1::electricity", "bus1": "mv2::electricity", "r": "1.2", "x": "3.5", "s_nom": "20", "num_parallel": "1", "length": "10"}
	for k, v := range want {
		if lines["l12"][k] != v {
			t.Errorf("lines.csv l12 %s: got %q want %q (row %v)", k, lines["l12"][k], v, lines["l12"])
		}
	}
	trafos := table(t, dir, "transformers.csv")
	want = map[string]string{"bus0": "hv::electricity", "bus1": "mv1::electricity", "s_nom": "40", "r": "0.01", "x": "0.1", "num_parallel": "1", "tap_ratio": "1"}
	for k, v := range want {
		if trafos["trafo"][k] != v {
			t.Errorf("transformers.csv trafo %s: got %q want %q (row %v)", k, trafos["trafo"][k], v, trafos["trafo"])
		}
	}
	if exists(dir, "links.csv") {
		t.Errorf("electrical arcs must not also be emitted as links: %s", readFile(t, dir, "links.csv"))
	}

	gens := table(t, dir, "generators.csv")
	if gens["grid"]["control"] != "Slack" || gens["pv"]["control"] != "PQ" {
		t.Errorf("generator control: grid %q pv %q", gens["grid"]["control"], gens["pv"]["control"])
	}
	if gens["grid"]["p_nom"] != "100" || gens["grid"]["p_nom_extendable"] != "False" {
		t.Errorf("fixed capacities expected: %v", gens["grid"])
	}
	pset := table(t, dir, "generators-p_set.csv")
	if len(pset) != 4 {
		t.Fatalf("generators-p_set.csv: want 4 snapshots, got %d", len(pset))
	}
	if pset["2025-01-01 02:00:00"]["pv"] != "6" {
		t.Errorf("pv dispatch at the third snapshot: got %q (table %v)", pset["2025-01-01 02:00:00"]["pv"], pset)
	}
	if _, ok := pset["2025-01-01 00:00:00"]["grid"]; ok {
		t.Error("the slack has no dispatch column")
	}
	loads := table(t, dir, "loads-p_set.csv")
	if loads["2025-01-01 00:00:00"]["load1"] != "10" || loads["2025-01-01 00:00:00"]["load2"] != "6" {
		t.Errorf("demand still materializes as the load p_set: %v", loads)
	}
	for _, absent := range []string{"global_constraints.csv", "_constraints.json", "_scenarios.json"} {
		if exists(dir, absent) {
			t.Errorf("%s has no place in a power flow", absent)
		}
	}
}

// The branch rating falls back to the arc's installed capacity.
func TestPowerFlowRatingDefaultsToCapacity(t *testing.T) {
	dir, _, _ := emitPowerFlow(t, func(j *model.Job) {
		l := j.Model.Transmission["l12"]
		l.Electrical.SNom = nil
		j.Model.Transmission["l12"] = l
	})
	if got := table(t, dir, "lines.csv")["l12"]["s_nom"]; got != "20" {
		t.Errorf("s_nom should default to capacity.existing 20, got %q", got)
	}
}

// Storage and conversion carry their fixed dispatch too: a series becomes the
// component's p_set file, a scalar the p_set column; non-electrical carriers
// keep their canonical bus carrier.
func TestPowerFlowStorageAndConversionDispatch(t *testing.T) {
	dir, warns, _ := emitPowerFlow(t, func(j *model.Job) {
		j.Model.Carriers["heat"] = model.Carrier{}
		j.Model.Timeseries["batt_out"] = model.TimeSeries{Source: "inline", Values: []float64{-2, -1, 1, 2}}
		j.Model.Technologies["batt"] = model.Technology{Role: model.RoleStorage, Node: model.StringList{"mv1"},
			CarrierOut: model.StringList{"electricity"}, Capacity: &model.Capacity{Existing: 5},
			Storage: &model.Storage{MaxHours: fp(2)}, Dispatch: model.Series("batt_out")}
		j.Model.Technologies["boiler"] = model.Technology{Role: model.RoleConversion, Node: model.StringList{"mv1"},
			CarrierIn: model.StringList{"electricity"}, CarrierOut: model.StringList{"heat"},
			Capacity: &model.Capacity{Existing: 2}, Dispatch: model.Num(1.5)}
	})
	sus := table(t, dir, "storage_units.csv")
	if sus["batt"]["p_nom"] != "5" {
		t.Errorf("storage unit row: %v", sus["batt"])
	}
	spset := table(t, dir, "storage_units-p_set.csv")
	if spset["2025-01-01 00:00:00"]["batt"] != "-2" || spset["2025-01-01 03:00:00"]["batt"] != "2" {
		t.Errorf("storage dispatch series: %v", spset)
	}
	links := table(t, dir, "links.csv")
	if links["boiler"]["p_set"] != "1.5" || links["boiler"]["bus0"] != "mv1::electricity" {
		t.Errorf("conversion dispatch as link p_set: %v", links["boiler"])
	}
	buses := table(t, dir, "buses.csv")
	if buses["mv1::heat"]["carrier"] != "heat" || buses["mv1::heat"]["v_nom"] != "" {
		t.Errorf("a non-electrical bus keeps its carrier and has no voltage: %v", buses["mv1::heat"])
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "balances the electricity network only") && strings.Contains(w, "heat") {
			found = true
		}
	}
	if !found {
		t.Errorf("want the unbalanced-carriers warning, got %v", warns)
	}
}

// The driver gets the power-flow settings only in that mode, so every other
// run.py stays byte-identical to its golden.
func TestPowerFlowRunPyConfig(t *testing.T) {
	plan := func(t *testing.T, j *model.Job) string {
		t.Helper()
		run := t.TempDir()
		p, err := (pypsa.PyPSA{}).Plan(j, "input", target.RunDirs{RunDir: run, InputDir: "input", OutDir: "output"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Command[1] != "run.py" {
			t.Fatalf("plan command: %v", p.Command)
		}
		return readFile(t, run, "run.py")
	}
	// cfgLine is the configuration the driver parses: JSON handed to
	// json.loads, never pasted in as a Python literal (false is not False).
	cfgLine := func(t *testing.T, script string) string {
		t.Helper()
		for _, line := range strings.Split(script, "\n") {
			if strings.HasPrefix(line, "CFG = json.loads(r'''") && strings.HasSuffix(line, "''')") {
				return line
			}
		}
		t.Fatalf("run.py has no parsed CFG line:\n%.300s", script)
		return ""
	}
	_, _, j := emitPowerFlow(t, nil)
	script := plan(t, &j)
	cfg := cfgLine(t, script)
	for _, want := range []string{`"mode":"power_flow"`, `"power_flow":{"distribute_slack":false,"method":"newton_raphson","slack":"grid","tolerance":0.000001}`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("run.py CFG lacks %s:\n%s", want, cfg)
		}
	}
	if !strings.Contains(script, `MODE == "power_flow"`) {
		t.Error("the driver body has no power_flow branch")
	}

	_, _, j = emitPowerFlow(t, func(j *model.Job) {
		j.Experiment.PowerFlow.Method = model.PowerFlowLinear
		j.Experiment.PowerFlow.DistributeSlack = true // needs a dispatch on the slack
		j.Experiment.PowerFlow.Tolerance = fp(1e-4)
		g := j.Model.Technologies["grid"]
		g.Dispatch = model.Num(16)
		j.Model.Technologies["grid"] = g
	})
	if cfg := cfgLine(t, plan(t, &j)); !strings.Contains(cfg, `"power_flow":{"distribute_slack":true,"method":"linear","slack":"grid","tolerance":0.0001}`) {
		t.Errorf("explicit options not forwarded:\n%s", cfg)
	}

	// A plan-mode job carries no power_flow key at all.
	jp, err := scenarios.Load("pypsa_transmission.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ValidateFor(&jp, model.TargetPyPSA); err != nil {
		t.Fatal(err)
	}
	if cfg := cfgLine(t, plan(t, &jp)); strings.Contains(cfg, "power_flow") {
		t.Errorf("plan-mode CFG must not mention power_flow:\n%s", cfg)
	}
}

// Outside mode power_flow the same model emits as before: arcs are transport
// links, buses carry no voltage, no AC carrier is declared.
func TestElectricalParametersIgnoredInPlanMode(t *testing.T) {
	dir, warns, _ := emitPowerFlow(t, func(j *model.Job) {
		j.Experiment.Mode = model.ModePlan
		j.Experiment.PowerFlow = nil
		pv := j.Model.Technologies["pv"]
		pv.Dispatch = nil
		j.Model.Technologies["pv"] = pv
	})
	links := table(t, dir, "links.csv")
	if _, ok := links["l12"]; !ok {
		t.Errorf("l12 should be a transport link in plan mode: %v", links)
	}
	if _, ok := links["trafo"]; !ok {
		t.Errorf("trafo should be a transport link in plan mode: %v", links)
	}
	for _, absent := range []string{"lines.csv", "transformers.csv"} {
		if exists(dir, absent) {
			t.Errorf("%s must not be emitted in plan mode", absent)
		}
	}
	if strings.Contains(readFile(t, dir, "buses.csv"), "v_nom") || strings.Contains(readFile(t, dir, "carriers.csv"), "AC") {
		t.Error("plan mode must not emit voltage levels or the AC carrier")
	}
	if got := table(t, dir, "generators.csv")["grid"]; got["control"] != "" || got["p_set"] != "" {
		t.Errorf("plan mode generators carry no control/p_set: %v", got)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "power-flow inputs") && strings.Contains(w, "ignored") {
			found = true
		}
	}
	if !found {
		t.Errorf("want the unused-electrical warning, got %v", warns)
	}
}

// emitReactive validates and emits the reactive power-flow scenario.
func emitReactive(t *testing.T, mut func(j *model.Job)) (string, []string) {
	t.Helper()
	j, err := scenarios.Load("pypsa_power_flow_reactive.json")
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(&j)
	}
	warns, err := target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	return dir, warns
}

func near(got string, want float64) bool {
	f, err := strconv.ParseFloat(got, 64)
	return err == nil && math.Abs(f-want) < 1e-6
}

// Reactive power, voltage control, the shunt, the controlled link and the
// branch refinements land on their PyPSA attributes.
func TestPowerFlowEmitsReactiveNetwork(t *testing.T) {
	dir, _ := emitReactive(t, nil)

	buses := table(t, dir, "buses.csv")
	if buses["hv::electricity"]["v_mag_pu_set"] != "1.02" || buses["mv2::electricity"]["v_mag_pu_set"] != "1" {
		t.Errorf("voltage set-points on the slack and the pv bus: %v %v", buses["hv::electricity"], buses["mv2::electricity"])
	}
	if buses["mv1::electricity"]["v_mag_pu_set"] != "" {
		t.Errorf("a bus without voltage control carries no set-point: %v", buses["mv1::electricity"])
	}

	gens := table(t, dir, "generators.csv")
	if gens["pv"]["control"] != "PV" || gens["grid"]["control"] != "Slack" || gens["dg"]["control"] != "PQ" {
		t.Errorf("generator control: pv %q grid %q dg %q", gens["pv"]["control"], gens["grid"]["control"], gens["dg"]["control"])
	}
	// dg: 1 MW at power factor 0.9 supplies 0.4843 MVAr.
	if !near(gens["dg"]["q_set"], math.Tan(math.Acos(0.9))) {
		t.Errorf("dg q_set derived from the power factor: got %q", gens["dg"]["q_set"])
	}
	if gens["pv"]["q_set"] != "0" {
		t.Errorf("a pv generator has no fixed q_set (the solver sets it): %q", gens["pv"]["q_set"])
	}

	loads := table(t, dir, "loads.csv")
	if loads["load3"]["q_set"] != "1.3" {
		t.Errorf("static reactive set-point: %v", loads["load3"])
	}
	qset := table(t, dir, "loads-q_set.csv")
	if len(qset) != 4 {
		t.Fatalf("loads-q_set.csv: want 4 snapshots, got %d", len(qset))
	}
	// load1: 12 MW at 0.95 at the second snapshot -> 3.944 MVAr (derived from the demand series).
	if !near(qset["2025-01-01 01:00:00"]["load1"], 12*math.Tan(math.Acos(0.95))) {
		t.Errorf("derived q series for load1: %v", qset["2025-01-01 01:00:00"])
	}
	if qset["2025-01-01 01:00:00"]["load2"] != "2.3" {
		t.Errorf("reactive set-point series for load2: %v", qset["2025-01-01 01:00:00"])
	}

	shunts := table(t, dir, "shunt_impedances.csv")
	if s := shunts["mv1::shunt"]; s["bus"] != "mv1::electricity" || s["g"] != "0" || s["b"] != "0.001" {
		t.Errorf("shunt row: %v", shunts)
	}

	links := table(t, dir, "links.csv")
	if l := links["hvdc"]; l["bus0"] != "mv2::electricity" || l["bus1"] != "mv3::electricity" || l["p_set"] != "3" || l["p_nom"] != "30" {
		t.Errorf("controlled link from the arc dispatch: %v", l)
	}
	lines := table(t, dir, "lines.csv")
	if l := lines["l12"]; l["r"] != "1.2" || l["x"] != "3.5" || l["b"] != "0.0001" || l["length"] != "10" {
		t.Errorf("per-length impedance and admittance: %v", l)
	}
	trafos := table(t, dir, "transformers.csv")
	if trafos["trafo"]["phase_shift"] != "2.5" {
		t.Errorf("phase shift: %v", trafos["trafo"])
	}
}

// A series arc dispatch and a series reactive dispatch on a generator and a
// storage unit materialize into their own p_set / q_set files; a leading
// power factor flips the sign.
func TestPowerFlowReactiveSeriesMaterialize(t *testing.T) {
	dir, _ := emitReactive(t, func(j *model.Job) {
		j.Model.Timeseries["flow"] = model.TimeSeries{Source: "inline", Values: []float64{3, 3, 2, 2}}
		j.Model.Timeseries["qpv"] = model.TimeSeries{Source: "inline", Values: []float64{0, 0.5, 1, 1}}
		j.Model.Timeseries["batt"] = model.TimeSeries{Source: "inline", Values: []float64{2, 1, -1, -2}}
		h := j.Model.Transmission["hvdc"]
		h.Dispatch = model.Series("flow")
		j.Model.Transmission["hvdc"] = h
		pv := j.Model.Technologies["pv"]
		pv.Electrical = &model.TechElectrical{ReactiveDispatch: model.Series("qpv")}
		j.Model.Technologies["pv"] = pv
		j.Model.Technologies["battery"] = model.Technology{Role: model.RoleStorage, Node: model.StringList{"mv1"}, CarrierOut: model.StringList{"electricity"},
			Capacity: &model.Capacity{Existing: 4}, Storage: &model.Storage{MaxHours: fp(2)}, Dispatch: model.Series("batt"),
			Electrical: &model.TechElectrical{PowerFactor: fp(0.9), Leading: true}}
	})
	if got := table(t, dir, "links-p_set.csv")["2025-01-01 02:00:00"]["hvdc"]; got != "2" {
		t.Errorf("arc dispatch series: %q", got)
	}
	if got := table(t, dir, "generators-q_set.csv")["2025-01-01 01:00:00"]["pv"]; got != "0.5" {
		t.Errorf("generator reactive series: %q", got)
	}
	// 2 MW discharge at a leading 0.9 -> -0.9686 MVAr; -2 MW charge -> +0.9686.
	sq := table(t, dir, "storage_units-q_set.csv")
	if !near(sq["2025-01-01 00:00:00"]["battery"], -2*math.Tan(math.Acos(0.9))) || !near(sq["2025-01-01 03:00:00"]["battery"], 2*math.Tan(math.Acos(0.9))) {
		t.Errorf("leading power factor on a storage series: %v", sq)
	}
	if exists(dir, "loads-p_set.csv") != true {
		t.Error("demand series still materialize")
	}
}

// Outside mode power_flow the reactive fields and the arc dispatch are ignored
// like the rest of the electrical network: no q_set, no shunts, no p_set.
func TestReactiveFieldsIgnoredInPlanMode(t *testing.T) {
	dir, warns := emitReactive(t, func(j *model.Job) {
		j.Experiment.Mode = model.ModePlan
		j.Experiment.PowerFlow = nil
	})
	for _, absent := range []string{"shunt_impedances.csv", "loads-q_set.csv", "lines.csv", "transformers.csv"} {
		if exists(dir, absent) {
			t.Errorf("%s must not be emitted in plan mode", absent)
		}
	}
	if got := table(t, dir, "links.csv")["hvdc"]; got["p_set"] != "" {
		t.Errorf("arc dispatch must not fix the link in plan mode: %v", got)
	}
	if strings.Contains(readFile(t, dir, "loads.csv"), "q_set") || strings.Contains(readFile(t, dir, "buses.csv"), "v_mag_pu_set") {
		t.Error("plan mode must not emit reactive set-points or voltage set-points")
	}
	if !hasWarn(warns, "power-flow inputs", "ignored") {
		t.Errorf("want the ignored-inputs warning, got %v", warns)
	}
}

func hasWarn(ws []string, parts ...string) bool {
	for _, w := range ws {
		ok := true
		for _, p := range parts {
			if !strings.Contains(w, p) {
				ok = false
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// --- ADR-0010: the network inside the optimization -------------------------

func emitLOPF(t *testing.T, mut func(j *model.Job)) (string, model.Job) {
	t.Helper()
	j, err := scenarios.Load("pypsa_lopf.json")
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(&j)
	}
	if _, err := target.ValidateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	return dir, j
}

// With network.model linear_power_flow an optimization emits the AC network —
// buses with v_nom, Lines with their rating, expansion bounds, annualized
// investment and loading limit — while generators stay decision variables.
func TestLinearPowerFlowEmitsLinesIntoOptimization(t *testing.T) {
	dir, _ := emitLOPF(t, nil)
	buses := table(t, dir, "buses.csv")
	if buses["a::electricity"]["carrier"] != "AC" || buses["a::electricity"]["v_nom"] != "110" {
		t.Errorf("AC bus with voltage level expected: %v", buses["a::electricity"])
	}
	lines := table(t, dir, "lines.csv")
	ab := lines["ab"]
	if ab["s_nom"] != "30" || ab["s_nom_extendable"] != "True" || ab["s_nom_max"] != "100" || ab["s_max_pu"] != "0.9" || ab["x"] != "10" {
		t.Errorf("expandable line row: %v", ab)
	}
	// 20000 €/MW over 40 years at 5 %: the annuity is 1165.5 €/MW/a.
	if !near(ab["capital_cost"], 20000*0.05/(1-math.Pow(1.05, -40))) {
		t.Errorf("annualized line investment: got %q", ab["capital_cost"])
	}
	if bc := lines["bc"]; bc["s_nom_extendable"] != "False" || bc["capital_cost"] != "0" || bc["s_max_pu"] != "" {
		t.Errorf("fixed line row: %v", bc)
	}
	gens := table(t, dir, "generators.csv")
	if gens["cheap"]["control"] != "" || gens["cheap"]["p_set"] != "" || gens["cheap"]["q_set"] != "" {
		t.Errorf("an optimization keeps generators free: %v", gens["cheap"])
	}
	if exists(dir, "links.csv") {
		t.Errorf("every arc of this network is a line: %s", readFile(t, dir, "links.csv"))
	}
	for _, absent := range []string{"shunt_impedances.csv", "generators-p_set.csv"} {
		if exists(dir, absent) {
			t.Errorf("%s has no place in an optimization", absent)
		}
	}
}

// Transmission investment is priced on Links too, and the per-distance cost
// multiplies the distance; a cost that cannot be annualized is left out.
func TestTransmissionCapitalCost(t *testing.T) {
	dir, _ := emitLOPF(t, func(j *model.Job) {
		j.Experiment.Network = nil // transport model: links
		l := j.Model.Transmission["ab"]
		l.Costs = map[string]model.CostClass{"monetary": {InvestmentPerCapacity: fp(20000), InvestmentPerCapacityDistance: fp(100), FixedOM: fp(300)}}
		j.Model.Transmission["ab"] = l
		bc := j.Model.Transmission["bc"]
		bc.Costs = map[string]model.CostClass{"monetary": {InvestmentPerCapacity: fp(1000)}} // no lifetime: warned, not priced
		j.Model.Transmission["bc"] = bc
	})
	links := table(t, dir, "links.csv")
	// (20000 + 100 * 50 km) annualized + 300 fixed O&M.
	want := 25000*0.05/(1-math.Pow(1.05, -40)) + 300
	if !near(links["ab"]["capital_cost"], want) {
		t.Errorf("link capital_cost: got %q want %g", links["ab"]["capital_cost"], want)
	}
	if links["bc"]["capital_cost"] != "0" {
		t.Errorf("unannualizable investment must not be priced: %v", links["bc"])
	}
	if exists(dir, "lines.csv") {
		t.Error("the transport model emits no lines")
	}
}

// The driver's configuration carries the security outages as (component,
// name) pairs only when asked; results tables are written for every run.
func TestLinearPowerFlowRunPyConfig(t *testing.T) {
	_, j := emitLOPF(t, func(j *model.Job) {
		j.Experiment.Network.Security = &model.SecurityOptions{Outages: []string{"ab", "bc"}}
	})
	run := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Plan(&j, "input", target.RunDirs{RunDir: run, InputDir: "input", OutDir: "output"}); err != nil {
		t.Fatal(err)
	}
	script := readFile(t, run, "run.py")
	if !strings.Contains(cfgOf(t, script), `"security":{"outages":[{"component":"Line","name":"ab"},{"component":"Line","name":"bc"}]}`) {
		t.Errorf("security outages not forwarded:\n%.400s", script)
	}
	if !strings.Contains(script, "optimize_security_constrained") || !strings.Contains(script, `"results"`) {
		t.Error("the driver must run the security-constrained solve and write the results folder")
	}
	_, j = emitLOPF(t, nil)
	run = t.TempDir()
	if _, err := (pypsa.PyPSA{}).Plan(&j, "input", target.RunDirs{RunDir: run, InputDir: "input", OutDir: "output"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfgOf(t, readFile(t, run, "run.py")), `"security"`) {
		t.Error("no security block without outages")
	}
}

// cfgOf returns the configuration the driver parses (the JSON handed to
// json.loads), so a check looks at the run's settings and not at the body.
func cfgOf(t *testing.T, script string) string {
	t.Helper()
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, "CFG = json.loads(r'''") {
			return line
		}
	}
	t.Fatalf("run.py has no CFG line:\n%.300s", script)
	return ""
}

// --- ADR-0011: storage as a Store -------------------------------------------

func emitStore(t *testing.T, mut func(j *model.Job)) (string, model.Job) {
	t.Helper()
	j, err := scenarios.Load("pypsa_store.json")
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(&j)
	}
	if _, err := target.ValidateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	return dir, j
}

// An energy-sized storage becomes a Store on its own bus with a charger and
// a discharger Link; energy sizing and its cost sit on the Store, power
// sizing and its costs on the Links, and the structural constraints travel
// in the _storage.json sidecar.
func TestStoreTopology(t *testing.T) {
	dir, _ := emitStore(t, nil)
	stores := table(t, dir, "stores.csv")
	b := stores["battery"]
	want := map[string]string{"bus": "n1::electricity::battery", "carrier": "electricity", "e_nom": "0", "e_nom_extendable": "True", "e_nom_max": "40", "e_cyclic": "True", "e_min_pu": "0.1", "standing_loss": "0.001"}
	for k, v := range want {
		if b[k] != v {
			t.Errorf("stores.csv battery %s: got %q want %q (row %v)", k, b[k], v, b)
		}
	}
	// An annualized 2 €/MWh/a is taken as given.
	if b["capital_cost"] != "2" {
		t.Errorf("store capital_cost: %q", b["capital_cost"])
	}
	buses := table(t, dir, "buses.csv")
	if buses["n1::electricity::battery"]["carrier"] != "electricity" {
		t.Errorf("the store bus is declared: %v", buses)
	}
	links := table(t, dir, "links.csv")
	ch, dis := links["battery_charge"], links["battery_discharge"]
	if ch["bus0"] != "n1::electricity" || ch["bus1"] != "n1::electricity::battery" || ch["efficiency"] != "0.95" || ch["p_nom_extendable"] != "True" || ch["p_nom_max"] != "10" || ch["capital_cost"] != "0" {
		t.Errorf("charger link: %v", ch)
	}
	if dis["bus0"] != "n1::electricity::battery" || dis["bus1"] != "n1::electricity" || dis["efficiency"] != "0.95" || dis["marginal_cost"] != "1" {
		t.Errorf("discharger link: %v", dis)
	}
	if dis["capital_cost"] != "10" {
		t.Errorf("the power investment sits on the discharger: %q", dis["capital_cost"])
	}
	sus := table(t, dir, "storage_units.csv")
	if _, ok := sus["battery"]; ok {
		t.Error("a Store-based storage must not also be a StorageUnit")
	}
	if sus["pumped"]["max_hours"] != "6" || sus["pumped"]["state_of_charge_set"] != "0" {
		t.Errorf("the StorageUnit keeps its representation (series set-point in its file): %v", sus["pumped"])
	}
	if got := table(t, dir, "storage_units-state_of_charge_set.csv")["2025-01-01 00:00:00"]["pumped"]; got != "12" {
		t.Errorf("soc profile series: %q", got)
	}
	raw := readFile(t, dir, "_storage.json")
	for _, want := range []string{`"store": "battery"`, `"charge": "battery_charge"`, `"discharge": "battery_discharge"`, `"equal_power": true`, `"charge_rate": 0.5`, `"discharge_rate": 0.5`} {
		if !strings.Contains(raw, want) {
			t.Errorf("_storage.json lacks %s:\n%s", want, raw)
		}
	}
}

// A fixed energy capacity fixes e_initial from initial_soc; an extendable one
// cannot and says so; a scalar soc_profile lands as a column.
func TestStoreFixedEnergy(t *testing.T) {
	dir, _ := emitStore(t, func(j *model.Job) {
		b := j.Model.Technologies["battery"]
		b.Capacity = &model.Capacity{Existing: 5}
		b.Storage.EnergyCapacity = &model.Capacity{Existing: 20}
		b.Storage.InitialSOC = fp(0.5)
		b.Storage.MaxChargeRate, b.Storage.MaxDischargeRate = nil, nil
		j.Model.Technologies["battery"] = b
		p := j.Model.Technologies["pumped"]
		p.Storage.SOCProfile = model.Num(10)
		j.Model.Technologies["pumped"] = p
	})
	b := table(t, dir, "stores.csv")["battery"]
	if b["e_nom"] != "20" || b["e_nom_extendable"] != "False" || b["e_initial"] != "10" {
		t.Errorf("fixed store: %v", b)
	}
	if links := table(t, dir, "links.csv"); links["battery_charge"]["p_nom"] != "5" || links["battery_charge"]["p_nom_extendable"] != "False" {
		t.Errorf("fixed links: %v", links["battery_charge"])
	}
	if !strings.Contains(readFile(t, dir, "_storage.json"), `"equal_power": false`) {
		t.Error("no equal-power constraint when the power is fixed")
	}
	if table(t, dir, "storage_units.csv")["pumped"]["state_of_charge_set"] != "10" {
		t.Error("scalar soc profile as a column")
	}
	if exists(dir, "storage_units-state_of_charge_set.csv") {
		t.Error("no series file for a scalar profile")
	}
}

// state_of_charge_initial is absolute in PyPSA: the canonical fraction over
// the installed energy (p_nom x max_hours). The pumped storage of the Store
// scenario starts half full of 4 MW x 6 h.
func TestInitialStateOfChargeIsAbsolute(t *testing.T) {
	dir, _ := emitStore(t, nil)
	if got := table(t, dir, "storage_units.csv")["pumped"]["state_of_charge_initial"]; got != "12" {
		t.Errorf("state_of_charge_initial: got %q, want 12 MWh (0.5 x 4 MW x 6 h)", got)
	}
}

// --- ADR-0011: modules, retirement, slack demand, copperplate ---------------

func emitScenario(t *testing.T, file string, mut func(j *model.Job)) (string, model.Job) {
	t.Helper()
	j, err := scenarios.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(&j)
	}
	if _, err := target.ValidateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	return dir, j
}

// capacity.per_unit on an expandable component is PyPSA's integer module.
func TestModularCapacity(t *testing.T) {
	dir, _ := emitScenario(t, "pypsa_modular.json", nil)
	g := table(t, dir, "generators.csv")["modular"]
	if g["p_nom_mod"] != "7" || g["p_nom_max"] != "70" || g["p_nom_extendable"] != "True" {
		t.Errorf("modular generator: %v", g)
	}
	if p := table(t, dir, "generators.csv")["peaker"]; p["p_nom_mod"] != "" {
		t.Errorf("a fixed generator has no module: %v", p)
	}
	st := table(t, dir, "stores.csv")["battery"]
	if st["e_nom_mod"] != "5" || st["e_nom_max"] != "20" {
		t.Errorf("modular store: %v", st)
	}
	links := table(t, dir, "links.csv")
	if links["battery_charge"]["p_nom_mod"] != "2" || links["battery_discharge"]["p_nom_mod"] != "2" {
		t.Errorf("modular store links: %v %v", links["battery_charge"], links["battery_discharge"])
	}
	dir, _ = emitScenario(t, "pypsa_lopf.json", func(j *model.Job) {
		l := j.Model.Transmission["ab"]
		l.Capacity.PerUnit = fp(25)
		j.Model.Transmission["ab"] = l
	})
	if ln := table(t, dir, "lines.csv")["ab"]; ln["s_nom_mod"] != "25" {
		t.Errorf("modular line: %v", ln)
	}
}

// Continuous retirement splits a component into its existing part, retirable
// down to zero at its fixed O&M, and a new-build part carrying the investment.
func TestContinuousRetirement(t *testing.T) {
	dir, _ := emitScenario(t, "pypsa_retire_slack.json", nil)
	gens := table(t, dir, "generators.csv")
	old := gens["old"]
	if old["p_nom"] != "60" || old["p_nom_extendable"] != "True" || old["p_nom_max"] != "60" || old["p_nom_min"] != "" || old["capital_cost"] != "100" || old["marginal_cost"] != "60" {
		t.Errorf("retirable existing part: %v", old)
	}
	if _, ok := gens["old_new"]; ok {
		t.Error("a non-expandable retirable component has no new-build part")
	}
	// Expandable too: the new-build part carries the investment above the
	// existing capacity.
	dir, _ = emitScenario(t, "pypsa_retire_slack.json", func(j *model.Job) {
		o := j.Model.Technologies["old"]
		o.Capacity.Expandable = true
		o.Capacity.Max = fp(100)
		o.Costs = map[string]model.CostClass{"monetary": {InvestmentPerCapacity: fp(1000), FixedOM: fp(100), VariableOM: model.Num(60)}}
		o.Operation = &model.Operation{MaxPU: model.Series("avail")}
		if j.Model.Timeseries == nil {
			j.Model.Timeseries = map[string]model.TimeSeries{}
		}
		j.Model.Timeseries["avail"] = model.TimeSeries{Source: "inline", Values: []float64{1, 1, 0.5, 0.5}}
		j.Model.Technologies["old"] = o
	})
	gens = table(t, dir, "generators.csv")
	old, nw := gens["old"], gens["old_new"]
	if old["p_nom_max"] != "60" || old["capital_cost"] != "100" {
		t.Errorf("existing part keeps fixed O&M only: %v", old)
	}
	if nw["p_nom"] != "0" || nw["p_nom_extendable"] != "True" || nw["p_nom_max"] != "40" || nw["capital_cost"] != "1100" || nw["marginal_cost"] != "60" {
		t.Errorf("new-build part: %v", nw)
	}
	avail := table(t, dir, "generators-p_max_pu.csv")
	if avail["2025-01-01 02:00:00"]["old"] != "0.5" || avail["2025-01-01 02:00:00"]["old_new"] != "0.5" {
		t.Errorf("series apply to both parts: %v", avail["2025-01-01 02:00:00"])
	}
}

// allow_unmet_demand adds a priced slack generator at every load bus under
// its own carrier; copperplate joins every node's bus of a carrier through a
// free hub and drops the arcs.
func TestSlackDemandAndCopperplate(t *testing.T) {
	dir, _ := emitScenario(t, "pypsa_retire_slack.json", nil)
	gens := table(t, dir, "generators.csv")
	for _, bus := range []string{"a::electricity", "b::electricity"} {
		s := gens[bus+"::unmet"]
		if s["bus"] != bus || s["carrier"] != "unmet" || s["marginal_cost"] != "500" || s["p_nom_extendable"] != "True" || s["capital_cost"] != "0" {
			t.Errorf("slack generator at %s: %v", bus, s)
		}
	}
	if _, ok := table(t, dir, "carriers.csv")["unmet"]; !ok {
		t.Error("the unmet carrier must be declared")
	}

	dir, _ = emitScenario(t, "pypsa_copperplate.json", nil)
	buses := table(t, dir, "buses.csv")
	if _, ok := buses["copperplate::electricity"]; !ok {
		t.Errorf("copperplate hub bus expected: %v", buses)
	}
	links := table(t, dir, "links.csv")
	for _, node := range []string{"n1", "n2", "n3"} {
		l := links[node+"::electricity::copperplate"]
		if l["bus0"] != node+"::electricity" || l["bus1"] != "copperplate::electricity" || l["p_nom_extendable"] != "True" || l["p_min_pu"] != "-1" || l["capital_cost"] != "0" {
			t.Errorf("copperplate link of %s: %v", node, l)
		}
	}
	if _, ok := links["l12"]; ok {
		t.Error("copperplate drops the model's own arcs")
	}
}

// --- ADR-0011: unit-commitment extras, quadratic cost, investment periods ----

func TestUnitCommitmentExtrasAndQuadraticColumns(t *testing.T) {
	dir, _ := emitScenario(t, "pypsa_uc_extras.json", nil)
	g := table(t, dir, "generators.csv")["ccgt"]
	want := map[string]string{"committable": "True", "ramp_limit_start_up": "0.6", "ramp_limit_shut_down": "0.6", "stand_by_cost": "5", "up_time_before": "2", "min_up_time": "2"}
	for k, v := range want {
		if g[k] != v {
			t.Errorf("ccgt %s: got %q want %q", k, g[k], v)
		}
	}
	dir, _ = emitScenario(t, "pypsa_quadratic.json", nil)
	if q := table(t, dir, "generators.csv")["quad"]; q["marginal_cost_quadratic"] != "0.5" || q["marginal_cost"] != "10" {
		t.Errorf("quadratic cost: %v", q)
	}
}

// Investment periods: the snapshot index becomes (period, timestep) with the
// series repeated per period, investment_periods.csv carries the years and
// weights, fixed components carry build_year and lifetime, and an expandable
// technology gets one component per period to build in.
func TestMultiPeriodEmission(t *testing.T) {
	dir, j := emitScenario(t, "pypsa_multi_period.json", nil)
	raw := readFile(t, dir, "investment_periods.csv")
	if !strings.Contains(raw, "period,objective,years") || !strings.Contains(raw, "2030,1,10") || !strings.Contains(raw, "2040,1,10") {
		t.Errorf("investment_periods.csv:\n%s", raw)
	}
	snaps := readFile(t, dir, "snapshots.csv")
	if !strings.HasPrefix(snaps, ",period,timestep") || strings.Count(snaps, "\n") != 5 || !strings.Contains(snaps, "2,2040,2025-01-01 00:00:00") {
		t.Errorf("snapshots.csv:\n%s", snaps)
	}
	gens := table(t, dir, "generators.csv")
	if o := gens["old"]; o["build_year"] != "2020" || o["lifetime"] != "15" || o["p_nom"] != "50" {
		t.Errorf("fixed generator with build year and lifetime: %v", o)
	}
	if _, ok := gens["new"]; ok {
		t.Error("an expandable technology is built per period, not once")
	}
	for _, y := range []string{"2030", "2040"} {
		n := gens["new-"+y]
		if n["build_year"] != y || n["lifetime"] != "20" || n["p_nom_extendable"] != "True" || n["p_nom_max"] != "100" {
			t.Errorf("per-period build %s: %v", y, n)
		}
	}
	// Series repeat per period and are keyed by position like PyPSA writes them.
	j.Model.Technologies["load"] = model.Technology{Role: model.RoleDemand, Node: model.StringList{"n1"}, CarrierIn: model.StringList{"electricity"},
		DemandProfile: &model.Value{Inline: []float64{40, 45}}}
	if _, err := target.ValidateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatal(err)
	}
	pset := readFile(t, dir, "loads-p_set.csv")
	if pset != ",load\n0,40\n1,45\n2,40\n3,45\n" {
		t.Errorf("loads-p_set.csv under periods:\n%s", pset)
	}
	run := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Plan(&j, "input", target.RunDirs{RunDir: run, InputDir: "input", OutDir: "output"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfgOf(t, readFile(t, run, "run.py")), `"multi_period":true`) {
		t.Error("the driver must solve with multi_investment_periods")
	}
}
