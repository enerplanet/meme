// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa_test

import (
	"encoding/csv"
	"os"
	"path/filepath"
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
		if strings.Contains(w, "electrical parameters") && strings.Contains(w, "only used by a power flow") {
			found = true
		}
	}
	if !found {
		t.Errorf("want the unused-electrical warning, got %v", warns)
	}
}
