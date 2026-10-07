// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target/pypsa"
)

// powerFlowSample returns a job carrying a top-level PowerFlow block with a
// small but realistic electrical network.
func powerFlowSample(t *testing.T) model.Job {
	t.Helper()
	return model.Job{
		Model: model.Model{
			Metadata: model.Metadata{Name: "pf-sample"},
			Time:     model.TimeConfig{},
		},
		PowerFlow: &model.PowerFlow{
			Buses: []model.PowerFlowBus{
				{Name: "n1", VNomKV: 0.4},
				{Name: "ntrafo_0", VNomKV: 0.4},
				{Name: "ntrafo_0_mv", VNomKV: 20},
			},
			Lines: []model.PowerFlowLine{
				{Name: "lv_0", Bus0: "n1", Bus1: "ntrafo_0", LengthKm: 0.05,
					NumParallel: 1, ROhm: 0.0104, XOhm: 0.004, SNomMVA: 0.15},
			},
			Transformers: []model.PowerFlowTransformer{
				{Name: "trafo_0", Bus0: "ntrafo_0_mv", Bus1: "ntrafo_0", SNomMVA: 0.4, NumParallel: 1},
			},
			Generators: []model.PowerFlowGenerator{
				{Name: "grid", Bus: "ntrafo_0_mv", Control: "Slack", PSetMW: []float64{1.0, 2.0}},
			},
			Loads: []model.PowerFlowLoad{
				{Name: "n1_load", Bus: "n1", PSetMW: []float64{0.5, 0.3}},
			},
		},
	}
}

// TestPowerFlowJobDecodes verifies the top-level power_flow key survives the
// Job decode (decodeJobRaw uses DisallowUnknownFields, so an unknown field
// would reject the whole job).
func TestPowerFlowJobDecodes(t *testing.T) {
	payload := `{"api_key":"k","model":{"metadata":{"name":"pf-sample"}},"experiment":{"mode":"plan"},"power_flow":{"buses":[{"name":"n1","v_nom_kv":0.4}],"lines":[{"name":"lv_0","bus0":"n1","bus1":"ntrafo_0","length_km":0.05,"num_parallel":1,"r_ohm":0.0104,"x_ohm":0.004,"s_nom_mva":0.15}],"transformers":[{"name":"trafo_0","bus0":"ntrafo_0_mv","bus1":"ntrafo_0","s_nom_mva":0.4,"num_parallel":1}],"generators":[{"name":"grid","bus":"ntrafo_0_mv","control":"Slack","p_set_mw":[1.0,2.0]}],"loads":[{"name":"n1_load","bus":"n1","p_set_mw":[0.5,0.3]}]}}`
	var job model.Job
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&job); err != nil {
		t.Fatalf("power_flow job must decode: %v", err)
	}
	if job.PowerFlow == nil {
		t.Fatal("job.PowerFlow must be populated")
	}
	if len(job.PowerFlow.Lines) != 1 || job.PowerFlow.Lines[0].Name != "lv_0" {
		t.Fatalf("unexpected lines: %+v", job.PowerFlow.Lines)
	}
}

// TestEmitterPowerFlowWritesNetworkCSVs verifies the emitter, when the job
// carries a PowerFlow block, writes the solvable network CSVs (lines.csv,
// transformers.csv, buses.csv with v_nom) + the p_set series.
func TestEmitterPowerFlowWritesNetworkCSVs(t *testing.T) {
	j := powerFlowSample(t)
	dir := t.TempDir()
	out, err := (pypsa.Emitter{}).Emit(&j, dir)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if out != dir {
		t.Fatalf("emit returned %q, want %q", out, dir)
	}

	for _, f := range []string{"network.csv", "carriers.csv", "buses.csv", "lines.csv",
		"transformers.csv", "generators.csv", "loads.csv", "snapshots.csv",
		"generators-p_set.csv", "loads-p_set.csv"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("emitter must write %s: %v", f, err)
		}
	}
	if !strings.Contains(readFile(t, dir, "buses.csv"), "v_nom") {
		t.Fatalf("buses.csv missing v_nom:\n%s", readFile(t, dir, "buses.csv"))
	}
	if !strings.Contains(readFile(t, dir, "lines.csv"), "n1,ntrafo_0") {
		t.Fatalf("lines.csv missing bus endpoints:\n%s", readFile(t, dir, "lines.csv"))
	}
	if !strings.Contains(readFile(t, dir, "lines.csv"), "0.0104") {
		t.Fatalf("lines.csv missing literal r_ohm:\n%s", readFile(t, dir, "lines.csv"))
	}
	if !strings.Contains(readFile(t, dir, "transformers.csv"), "ntrafo_0_mv,ntrafo_0") {
		t.Fatalf("transformers.csv missing trafo endpoints:\n%s", readFile(t, dir, "transformers.csv"))
	}
	if !strings.Contains(readFile(t, dir, "generators-p_set.csv"), "snapshot,grid") ||
		!strings.Contains(readFile(t, dir, "generators-p_set.csv"), "2") {
		t.Fatalf("generators-p_set.csv missing grid series:\n%s", readFile(t, dir, "generators-p_set.csv"))
	}
	if !strings.Contains(readFile(t, dir, "loads-p_set.csv"), "snapshot,n1_load") {
		t.Fatalf("loads-p_set.csv missing n1_load series:\n%s", readFile(t, dir, "loads-p_set.csv"))
	}
}

// TestRunPyForcesPowerFlowMode verifies the generated run.py CFG forces the
// power_flow mode when a PowerFlow block is present (the backend sends
// experiment.mode 'plan').
func TestRunPyForcesPowerFlowMode(t *testing.T) {
	j := powerFlowSample(t)
	script := planRunPy(t, j)
	if !strings.Contains(script, `"mode":"power_flow"`) {
		t.Fatalf("run.py CFG must force power_flow mode:\n%s", script)
	}
	// Sanity: the PF branch is present in the embedded driver.
	if !strings.Contains(script, `MODE == "power_flow"`) {
		t.Fatalf("run.py must carry the power_flow branch:\n%s", script)
	}
}
