// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package e2e_test drives the whole service end-to-end with REAL framework
// execution: it boots the HTTP API with a CommandRunner, submits scenario
// payloads to /simulate, polls /jobs/{id}/status, and then verifies the four
// contract points the service promises:
//
//  1. the native model files are written to the hard disk,
//  2. the framework process is actually executed (exit code 0, real output),
//  3. the solver returns results (optimal solve, result files on disk),
//  4. the result zip bundles the model results, the initial config, the
//     generated files, and the logs.
//
// These tests need PyPSA, Calliope, AdOpT-NET0 and their solvers on PATH, so
// they only run when MEME_E2E=1 (set inside the environment/ container).
//
// The suite is tiered:
//
//	go test -short ./test/e2e   smoke: the three per-target lifecycles and the
//	                            multi-target job (~1 min) — always run these.
//	go test ./test/e2e          full: adds the economics pins, the cross-target
//	                            consistency check and the whole scenario corpus.
//
// Independent tests and the corpus subtests are t.Parallel(); bound the solver
// process count with -parallel N (the Makefile uses 3).
package e2e_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/enerplanet/meme/internal/api"
	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/service"
)

// pollTimeout bounds one job's queued->done wait. AdOpT jobs (read_data +
// technology fitting + N solves for monte carlo) are the slowest.
const pollTimeout = 15 * time.Minute

func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("MEME_E2E") != "1" {
		t.Skip("set MEME_E2E=1 (inside the environment container) to run end-to-end tests")
	}
}

// requireFull additionally skips the test in -short (smoke) runs.
func requireFull(t *testing.T) {
	t.Helper()
	requireE2E(t)
	if testing.Short() {
		t.Skip("skipped in -short smoke runs (drop -short for the full suite)")
	}
}

// newE2EServer boots the real HTTP API with real command execution and a
// test-owned work root we can inspect on disk.
func newE2EServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	work := t.TempDir()
	srv := httptest.NewServer(api.NewServer(api.Server{
		Executor: service.NewExecutor(service.CommandRunner{Timeout: 12 * time.Minute}, 2),
		WorkRoot: work,
	}))
	t.Cleanup(srv.Close)
	return srv, work
}

func readScenario(t *testing.T, file string) []byte {
	t.Helper()
	b, err := scenarios.Raw(file)
	if err != nil {
		t.Fatalf("read scenario %s: %v", file, err)
	}
	return b
}

// submit POSTs a payload to /simulate and returns the decoded 202 body.
func submit(t *testing.T, base string, target model.Target, payload []byte) map[string]any {
	t.Helper()
	resp, err := http.Post(base+"/simulate?target="+string(target), "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /simulate: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /simulate: status %d, body %s", resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode submit response: %v (%s)", err, body)
	}
	if out["scheduled"] != true {
		t.Fatalf("submit not scheduled: %v", out)
	}
	if id, _ := out["id"].(string); id == "" {
		t.Fatalf("submit returned no job id: %v", out)
	}
	for _, k := range []string{"status_url", "result_url"} {
		if s, _ := out[k].(string); s == "" {
			t.Errorf("submit response missing %s", k)
		}
	}
	return out
}

// awaitJob polls the status endpoint until the job reaches a terminal state.
func awaitJob(t *testing.T, base, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(pollTimeout)
	var status map[string]any
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/jobs/" + id + "/status")
		if err != nil {
			t.Fatalf("GET status: %v", err)
		}
		status = map[string]any{}
		err = json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("decode status: %v", err)
		}
		if s, _ := status["state"].(string); s == string(service.StateSucceeded) || s == string(service.StateFailed) {
			return status
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish within %s (last status: %v)", id, pollTimeout, status)
	return nil
}

// fetchZip downloads the finished job's result bundle and returns its entries.
func fetchZip(t *testing.T, base, id string) map[string][]byte {
	t.Helper()
	resp, err := http.Get(base + "/jobs/" + id)
	if err != nil {
		t.Fatalf("GET result: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET result: status %d, body %s", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("result Content-Type = %q, want application/zip", ct)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read zip body: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("zip entry %s: %v", f.Name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = b
	}
	return files
}

func zipNames(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for n := range files {
		out = append(out, n)
	}
	return out
}

// hasEntryWithPrefix reports whether any zip entry starts with prefix.
func hasEntryWithPrefix(files map[string][]byte, prefix string) bool {
	for n := range files {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// runsOf decodes the runs list out of a status/metadata JSON blob.
func runsOf(t *testing.T, status map[string]any) []map[string]any {
	t.Helper()
	raw, ok := status["runs"].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("status has no runs: %v", status)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, _ := r.(map[string]any)
		out = append(out, m)
	}
	return out
}

// jobDirOf finds the job's work directory on disk (job-* under the work root).
func jobDirOf(t *testing.T, workRoot string) string {
	t.Helper()
	entries, err := os.ReadDir(workRoot)
	if err != nil {
		t.Fatalf("read work root: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "job-") {
			return filepath.Join(workRoot, e.Name())
		}
	}
	t.Fatalf("no job-* dir under %s (entries: %v)", workRoot, entries)
	return ""
}

func mustExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if fi, err := os.Stat(p); err != nil {
			t.Errorf("expected file on disk, missing: %s (%v)", p, err)
		} else if fi.Size() == 0 {
			t.Errorf("file on disk but empty: %s", p)
		}
	}
}

// objectivePin pins a solver objective: the run's value must land within
// value ± value*rel. rel is the relative delta absorbing solver numerics
// (tolerance scales with the objective's magnitude); value 0 disables the pin.
type objectivePin struct {
	value float64 // expected objective (0 = no pin)
	rel   float64 // allowed relative deviation, e.g. 1e-4 = ±0.01%
}

func (p objectivePin) check(t *testing.T, file string, obj float64) {
	t.Helper()
	if delta := math.Abs(obj - p.value); delta > math.Abs(p.value)*p.rel {
		t.Errorf("%s: objective = %v, want %v ±%v%% — the translation drifted",
			file, obj, p.value, p.rel*100)
	}
}

// lifecycle runs one scenario through the full submit -> poll -> zip pipeline
// with all four contract checks. diskFiles are paths relative to the job dir
// that must exist on the hard disk; markers must appear in the job log.
// pin (if set) bounds the solver's objective value — these are deterministic
// LPs, so a drift beyond the pin's delta means a translation regression, not
// solver noise.
func lifecycle(t *testing.T, file string, target model.Target, diskFiles, logMarkers []string, pin objectivePin) {
	srv, work := newE2EServer(t)
	payload := readScenario(t, file)

	sub := submit(t, srv.URL, target, payload)
	id := sub["id"].(string)

	status := awaitJob(t, srv.URL, id)
	logText, _ := status["log"].(string)
	if status["state"] != string(service.StateSucceeded) {
		t.Fatalf("%s: job failed.\nerror: %v\nlog:\n%s", file, status["error"], logText)
	}
	if obj, ok := anyObjectiveFromLog(logText); ok {
		t.Logf("%s: objective %v", file, obj)
		if pin.value != 0 {
			pin.check(t, file, obj)
		}
	} else if pin.value != 0 {
		t.Errorf("%s: no objective value found in log", file)
	}

	// (2) framework executed: every run exited 0 and produced real output.
	for _, run := range runsOf(t, status) {
		if ec, _ := run["exit_code"].(float64); ec != 0 {
			t.Errorf("%s: run exit_code = %v, want 0 (stderr: %v)", file, ec, run["stderr"])
		}
		if s, _ := run["stdout"].(string); strings.TrimSpace(s) == "" {
			t.Errorf("%s: run produced no stdout — was the framework executed?", file)
		} else if strings.Contains(s, "dry-run") {
			t.Errorf("%s: run was a dry-run, not a real execution", file)
		}
	}
	for _, m := range logMarkers {
		if !strings.Contains(logText, m) {
			t.Errorf("%s: job log missing solver marker %q", file, m)
		}
	}

	// (1)+(3) files on the hard disk: emitted native inputs and solver results.
	jobDir := jobDirOf(t, work)
	paths := make([]string, 0, len(diskFiles)+1)
	paths = append(paths, filepath.Join(jobDir, "config.json"))
	for _, f := range diskFiles {
		paths = append(paths, filepath.Join(jobDir, f))
	}
	mustExist(t, paths...)

	// (4) the zip bundle: results, initial config, generated files, logs.
	files := fetchZip(t, srv.URL, id)
	for _, want := range []string{"metadata.json", "log.txt", "results.json", "config.json"} {
		if _, ok := files[want]; !ok {
			t.Errorf("%s: zip missing %s (have %v)", file, want, zipNames(files))
		}
	}
	// initial config must round-trip byte-identical.
	if got, ok := files["config.json"]; ok && !bytes.Equal(got, payload) {
		t.Errorf("%s: zip config.json differs from the submitted payload", file)
	}
	// the generated input tree and the solver results must be in the bundle.
	if !hasEntryWithPrefix(files, "files/run_0/input/") {
		t.Errorf("%s: zip missing generated files under files/run_0/input/", file)
	}
	for _, f := range diskFiles {
		name := "files/" + filepath.ToSlash(f)
		if _, ok := files[name]; !ok {
			t.Errorf("%s: zip missing %s", file, name)
		}
	}
	// the log in the zip matches what the status endpoint reported.
	if lg, ok := files["log.txt"]; ok && !strings.Contains(string(lg), "all runs complete") {
		t.Errorf("%s: zip log.txt does not record run completion:\n%s", file, lg)
	}
}

// --- deep per-target lifecycle tests (the smoke tier) --------------------------

// Pinned objective values for the three lifecycle scenarios, recorded from a
// verified run on 2026-07-09 (PyPSA 1.2.4/HiGHS, Calliope 0.7.0.dev7/CBC,
// AdOpT 0.1.10/GLPK). Each pin carries its own relative delta (rel) bounding
// acceptable solver numerics; a drift beyond it is a translation regression.
// value 0 disables a pin until a number has been recorded.
var (
	pypsaLifecycleObjective    = objectivePin{value: 4.2571474e6, rel: 1e-4}
	calliopeLifecycleObjective = objectivePin{value: 2915.8544, rel: 1e-4}
	adoptLifecycleObjective    = objectivePin{value: 21000, rel: 1e-4}
)

func TestE2EPyPSALifecycle(t *testing.T) {
	requireE2E(t)
	t.Parallel()
	lifecycle(t, "pypsa_generators.json", model.TargetPyPSA,
		[]string{
			"run_0/input/buses.csv",
			"run_0/input/generators.csv",
			"run_0/input/loads.csv",
			"run_0/output/network.nc", // solved network exported by the runner
		},
		[]string{"status ok", "objective"},
		pypsaLifecycleObjective,
	)
}

func TestE2ECalliopeLifecycle(t *testing.T) {
	requireE2E(t)
	t.Parallel()
	lifecycle(t, "calliope_plan.json", model.TargetCalliope,
		[]string{
			"run_0/input/model.yaml",
			"run_0/output/results.nc", // --save_netcdf
		},
		[]string{"Calliope run complete", "Saving NetCDF results", "exit=0"},
		calliopeLifecycleObjective,
	)
}

func TestE2EAdOptLifecycle(t *testing.T) {
	requireE2E(t)
	t.Parallel()
	lifecycle(t, "adopt_storage.json", model.TargetAdOpt,
		[]string{
			"run_0/input/input_data/Topology.json",
			"run_0/input/input_data/ConfigModel.json",
		},
		// glpk prints "... OPTIMAL ... SOLUTION FOUND" on a successful solve.
		[]string{"OPTIMAL", "SOLUTION FOUND", "exit=0"},
		adoptLifecycleObjective,
	)
	// AdOpT writes its results below the configured save_path
	// (run_0/input/results); presence is asserted in TestE2EAdOptResultsOnDisk.
}

// TestE2EAdOptResultsOnDisk proves the AdOpT solver actually produced result
// artifacts (HDF5/summary) under the configured save path.
func TestE2EAdOptResultsOnDisk(t *testing.T) {
	requireFull(t)
	t.Parallel()
	srv, work := newE2EServer(t)
	sub := submit(t, srv.URL, model.TargetAdOpt, readScenario(t, "adopt_demand_trade.json"))
	status := awaitJob(t, srv.URL, sub["id"].(string))
	if status["state"] != string(service.StateSucceeded) {
		t.Fatalf("adopt job failed: %v\nlog:\n%v", status["error"], status["log"])
	}
	resultsDir := filepath.Join(jobDirOf(t, work), "run_0", "input", "results")
	var found []string
	_ = filepath.WalkDir(resultsDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			found = append(found, p)
		}
		return nil
	})
	if len(found) == 0 {
		t.Errorf("no AdOpT result files under %s — solver results not persisted", resultsDir)
	}
}

// TestE2ESweepProducesMultipleRuns checks the orchestration path the API
// promises: a sweep expands into independent runs, each with its own input and
// output on disk and in the bundle.
func TestE2ESweepProducesMultipleRuns(t *testing.T) {
	requireFull(t)
	t.Parallel()
	srv, _ := newE2EServer(t)

	var job map[string]any
	if err := json.Unmarshal(readScenario(t, "pypsa_generators.json"), &job); err != nil {
		t.Fatalf("decode scenario: %v", err)
	}
	exp := job["experiment"].(map[string]any)
	exp["sweep"] = []map[string]any{
		{"parameter": "technologies.pv.capacity.max", "values": []float64{200, 500}},
	}
	payload, _ := json.Marshal(job)

	sub := submit(t, srv.URL, model.TargetPyPSA, payload)
	status := awaitJob(t, srv.URL, sub["id"].(string))
	if status["state"] != string(service.StateSucceeded) {
		t.Fatalf("sweep job failed: %v\nlog:\n%v", status["error"], status["log"])
	}
	runs := runsOf(t, status)
	if len(runs) != 2 {
		t.Fatalf("sweep expanded into %d runs, want 2", len(runs))
	}
	files := fetchZip(t, srv.URL, sub["id"].(string))
	for _, prefix := range []string{"files/run_0/input/", "files/run_1/input/", "files/run_0/output/", "files/run_1/output/"} {
		if !hasEntryWithPrefix(files, prefix) {
			t.Errorf("sweep zip missing %s tree", prefix)
		}
	}
}

// TestE2ETradeExportRevenue pins the economics of trade export: with a cheap
// generator (5/MWh) and a lucrative export market (50/MWh, 200 MW limit), the
// optimum must export and the objective must turn negative (net revenue). This
// guards the marginal-cost sign convention of the export generator.
func TestE2ETradeExportRevenue(t *testing.T) {
	requireFull(t)
	t.Parallel()
	payload := []byte(`{
	  "model": {
	    "metadata": {"name": "trade_export_revenue"},
	    "time": {"start": "2025-01-01", "end": "2025-01-02", "resolution": "1H"},
	    "carriers": {"electricity": {"unit": "MWh"}},
	    "nodes": {"n1": {}},
	    "technologies": {
	      "gen": {"role": "supply", "node": "n1", "carrier_out": "electricity",
	              "capacity": {"expandable": false, "existing": 300},
	              "costs": {"monetary": {"variable_om": 5}}},
	      "load": {"role": "demand", "node": "n1", "carrier_in": "electricity", "demand_profile": 100}
	    },
	    "trade": {"market": {"node": "n1", "carrier": "electricity",
	                         "export": {"limit": 200, "price": 50}}}
	  },
	  "experiment": {"mode": "plan", "solver": {"name": "highs"}}
	}`)
	srv, _ := newE2EServer(t)
	sub := submit(t, srv.URL, model.TargetPyPSA, payload)
	status := awaitJob(t, srv.URL, sub["id"].(string))
	logText, _ := status["log"].(string)
	if status["state"] != string(service.StateSucceeded) {
		t.Fatalf("trade job failed: %v\nlog:\n%s", status["error"], logText)
	}
	obj, ok := objectiveFromLog(logText)
	if !ok {
		t.Fatalf("no objective in log:\n%s", logText)
	}
	// Optimal plan: serve 100 at cost 5 and export 200 at price 50 -> net
	// revenue. objective = 300*5 - 200*50 = -8500 per snapshot.
	if obj >= 0 {
		t.Errorf("objective = %v, want negative (export revenue must be exploited)", obj)
	}
}

// objectiveFromLog extracts the runner's "objective <value>" line.
func objectiveFromLog(log string) (float64, bool) {
	for _, line := range strings.Split(log, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && fields[0] == "objective" {
			if f, err := strconv.ParseFloat(fields[1], 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

// TestE2ESidecarConstraintBinds proves run.py applies _constraints.json for
// real: force_pv (capacity(pv) >= 300) makes the optimum build 300 MW of
// otherwise-uncompetitive PV, so the objective must carry its annualized capex
// (~300 * 35476 ≈ 10.6M) instead of the unconstrained gas-only dispatch (~500).
func TestE2ESidecarConstraintBinds(t *testing.T) {
	requireFull(t)
	t.Parallel()
	srv, _ := newE2EServer(t)
	sub := submit(t, srv.URL, model.TargetPyPSA, readScenario(t, "pypsa_constraints_sidecar.json"))
	status := awaitJob(t, srv.URL, sub["id"].(string))
	logText, _ := status["log"].(string)
	if status["state"] != string(service.StateSucceeded) {
		t.Fatalf("job failed: %v\nlog:\n%s", status["error"], logText)
	}
	if !strings.Contains(logText, "applied sidecar constraint force_pv") {
		t.Errorf("run.py did not report applying the sidecar constraint:\n%s", logText)
	}
	obj, ok := objectiveFromLog(logText)
	if !ok {
		t.Fatalf("no objective in log:\n%s", logText)
	}
	if obj < 1e6 {
		t.Errorf("objective = %v: the >=300 MW PV constraint did not bind (unconstrained optimum is ~500)", obj)
	}
}

// TestE2ETradePriceSeries pins time-varying import prices: with prices
// [10,200,10,200], demand 100 and local generation at 100/MWh, the optimum
// imports in cheap hours and generates in dear ones: 2*(100*10) + 2*(100*100)
// = 22000.
func TestE2ETradePriceSeries(t *testing.T) {
	requireFull(t)
	t.Parallel()
	srv, _ := newE2EServer(t)
	sub := submit(t, srv.URL, model.TargetPyPSA, readScenario(t, "pypsa_trade_price_series.json"))
	status := awaitJob(t, srv.URL, sub["id"].(string))
	logText, _ := status["log"].(string)
	if status["state"] != string(service.StateSucceeded) {
		t.Fatalf("job failed: %v\nlog:\n%s", status["error"], logText)
	}
	obj, ok := objectiveFromLog(logText)
	if !ok {
		t.Fatalf("no objective in log:\n%s", logText)
	}
	if obj < 21500 || obj > 22500 {
		t.Errorf("objective = %v, want ~22000 (a flat price would give 4000 or 40000+)", obj)
	}
}

// TestE2ECrossTargetConsistency runs the SAME dispatch-only model (demand
// served by grid import at 5/MWh over 24 hourly steps, total 2268 MWh) on all
// three frameworks and requires the three solvers to agree on the objective
// (5 * 2268 = 11340). This is the strongest guard on translation semantics:
// any unit, sign, or time-indexing drift between emitters shows up here.
func TestE2ECrossTargetConsistency(t *testing.T) {
	requireFull(t)
	t.Parallel()
	payload := []byte(`{
	  "model": {
	    "metadata": {"name": "cross_target"},
	    "time": {"start": "2025-01-01", "end": "2025-01-01T23:00", "resolution": "1H"},
	    "carriers": {"electricity": {"unit": "MWh"}},
	    "nodes": {"n1": {}},
	    "technologies": {
	      "load": {"role": "demand", "node": "n1", "carrier_in": "electricity", "demand_profile": "dem"}
	    },
	    "timeseries": {"dem": {"source": "inline", "values": [70, 65, 60, 58, 60, 70, 90, 110, 120, 115, 110, 105, 100, 100, 105, 110, 120, 125, 120, 110, 100, 90, 80, 75]}},
	    "trade": {"grid": {"node": "n1", "carrier": "electricity", "import": {"limit": 500, "price": 5}}}
	  },
	  "experiment": {"mode": "plan", "solver": {"name": "highs"}}
	}`)
	const want = 11340.0

	objectives := map[model.Target]float64{}
	for _, target := range []model.Target{model.TargetPyPSA, model.TargetCalliope, model.TargetAdOpt} {
		srv, _ := newE2EServer(t)
		sub := submit(t, srv.URL, target, payload)
		status := awaitJob(t, srv.URL, sub["id"].(string))
		logText, _ := status["log"].(string)
		if status["state"] != string(service.StateSucceeded) {
			t.Fatalf("%s: job failed: %v\nlog:\n%s", target, status["error"], logText)
		}
		obj, ok := anyObjectiveFromLog(logText)
		if !ok {
			t.Fatalf("%s: no objective found in log:\n%s", target, logText)
		}
		objectives[target] = obj
	}
	for target, obj := range objectives {
		if obj < want*0.99 || obj > want*1.01 {
			t.Errorf("%s: objective = %v, want %v +-1%% (all targets: %v)", target, obj, want, objectives)
		}
	}
}

// anyObjectiveFromLog extracts an objective value from whichever solver wrote
// the log: the PyPSA runner's "objective X", CBC's "Optimal - objective value
// X" (Calliope), or Pyomo's "Lower bound: X" (AdOpT/GLPK).
func anyObjectiveFromLog(log string) (float64, bool) {
	if f, ok := objectiveFromLog(log); ok {
		return f, true
	}
	for _, line := range strings.Split(log, "\n") {
		s := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(s, "Optimal - objective value "); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(rest), 64); err == nil {
				return f, true
			}
		}
		if rest, ok := strings.CutPrefix(s, "Lower bound: "); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(rest), 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

// TestE2EMultiTarget submits ONE shared config with ?target=all and requires
// all three framework models to be generated, executed, and solved in a single
// job, with the bundle carrying every target's inputs and solver results in
// per-target subtrees.
func TestE2EMultiTarget(t *testing.T) {
	requireE2E(t) // smoke tier: the multi-target contract always runs
	t.Parallel()
	payload, err := os.ReadFile("../../examples/shared_full.json")
	if err != nil {
		t.Fatalf("read shared example: %v", err)
	}
	srv, work := newE2EServer(t)

	resp, err := http.Post(srv.URL+"/simulate?target=all", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /simulate: %v", err)
	}
	var sub map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&sub)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || sub["scheduled"] != true {
		t.Fatalf("submit failed: %d %v", resp.StatusCode, sub)
	}
	if got, _ := sub["target"].(string); got != "adopt-net0,calliope,pypsa" {
		t.Errorf("submit target = %q, want the full list", got)
	}

	status := awaitJob(t, srv.URL, sub["id"].(string))
	logText, _ := status["log"].(string)
	if status["state"] != string(service.StateSucceeded) {
		t.Fatalf("multi-target job failed: %v\nlog:\n%s", status["error"], logText)
	}

	// One run set per target (the payload sweeps 2 points -> 2 runs each).
	perTarget := map[string]int{}
	for _, run := range runsOf(t, status) {
		perTarget[run["target"].(string)]++
		if ec, _ := run["exit_code"].(float64); ec != 0 {
			t.Errorf("run for %v exited %v", run["target"], ec)
		}
	}
	for _, target := range []string{"pypsa", "calliope", "adopt-net0"} {
		if perTarget[target] != 2 {
			t.Errorf("target %s: %d runs, want 2 (sweep)", target, perTarget[target])
		}
	}

	// All three native models and their solver results on disk, per target.
	jobDir := jobDirOf(t, work)
	mustExist(t,
		filepath.Join(jobDir, "config.json"),
		filepath.Join(jobDir, "pypsa", "run_0", "input", "generators.csv"),
		filepath.Join(jobDir, "pypsa", "run_0", "output", "network.nc"),
		filepath.Join(jobDir, "calliope", "run_0", "input", "model.yaml"),
		filepath.Join(jobDir, "calliope", "run_0", "output", "results.nc"),
		filepath.Join(jobDir, "adopt-net0", "run_0", "input", "input_data", "Topology.json"),
		filepath.Join(jobDir, "adopt-net0", "run_1", "input", "input_data", "Topology.json"),
	)

	// And all of it in the one zip.
	files := fetchZip(t, srv.URL, sub["id"].(string))
	for _, want := range []string{
		"metadata.json", "log.txt", "results.json", "config.json",
		"files/pypsa/run_0/output/network.nc",
		"files/calliope/run_0/output/results.nc",
		"files/pypsa/run_1/output/network.nc",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("zip missing %s", want)
		}
	}
	if !hasEntryWithPrefix(files, "files/adopt-net0/run_0/input/results/") {
		t.Errorf("zip missing adopt solver results tree")
	}
	// The bundle's config.json is the submitted payload with the api_key
	// stripped (handlers.redactAPIKey re-marshals top-level keys sorted with
	// two-space indent); a keyless payload stays byte-identical.
	if got, ok := files["config.json"]; ok && !bytes.Equal(got, redactedConfig(t, payload)) {
		t.Errorf("zip config.json differs from the submitted shared payload (api_key-redacted)")
	}
}

// redactedConfig mirrors the server's api_key redaction of a persisted config.
func redactedConfig(t *testing.T, payload []byte) []byte {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if _, ok := doc["api_key"]; !ok {
		return payload
	}
	delete(doc, "api_key")
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal redacted payload: %v", err)
	}
	return out
}

// TestE2EFailedRunIsReported checks the negative path: an infeasible model must
// end in state=failed with the solver's complaint preserved, not be reported as
// success.
func TestE2EFailedRunIsReported(t *testing.T) {
	requireFull(t)
	t.Parallel()
	srv, _ := newE2EServer(t)

	// Demand with no possible supply: generation capped to zero.
	var job map[string]any
	if err := json.Unmarshal(readScenario(t, "pypsa_generators.json"), &job); err != nil {
		t.Fatalf("decode scenario: %v", err)
	}
	techs := job["model"].(map[string]any)["technologies"].(map[string]any)
	for _, name := range []string{"pv", "ccgt"} {
		tech := techs[name].(map[string]any)
		tech["capacity"] = map[string]any{"expandable": false, "existing": 0, "max": 0}
	}
	payload, _ := json.Marshal(job)

	sub := submit(t, srv.URL, model.TargetPyPSA, payload)
	status := awaitJob(t, srv.URL, sub["id"].(string))
	if status["state"] != string(service.StateFailed) {
		t.Errorf("infeasible model reported state=%v, want failed", status["state"])
	}
}

// --- breadth: every scenario must run end-to-end ------------------------------

// TestE2EAllScenarios submits every catalogued scenario to its target and
// requires the full pipeline (validate -> emit -> execute -> solve) to succeed.
func TestE2EAllScenarios(t *testing.T) {
	requireFull(t)
	entries := scenarios.Names()
	targetOf := func(name string) model.Target {
		switch {
		case strings.HasPrefix(name, "pypsa_"):
			return model.TargetPyPSA
		case strings.HasPrefix(name, "calliope_"):
			return model.TargetCalliope
		case strings.HasPrefix(name, "adopt_"):
			return model.TargetAdOpt
		}
		return ""
	}
	for _, name := range entries {
		target := targetOf(name)
		if target == "" || !strings.HasSuffix(name, ".json") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel() // independent server + work dir per scenario; cap via -parallel
			srv, work := newE2EServer(t)
			sub := submit(t, srv.URL, target, readScenario(t, name))
			status := awaitJob(t, srv.URL, sub["id"].(string))
			if status["state"] != string(service.StateSucceeded) {
				t.Fatalf("%s (%s) failed: %v\nlog:\n%v", name, target, status["error"], status["log"])
			}
			for _, run := range runsOf(t, status) {
				if ec, _ := run["exit_code"].(float64); ec != 0 {
					t.Errorf("%s: run exit=%v", name, ec)
				}
			}
			// native model files really landed on disk for every run.
			jobDir := jobDirOf(t, work)
			input := filepath.Join(jobDir, "run_0", "input")
			if entries, err := os.ReadDir(input); err != nil || len(entries) == 0 {
				t.Errorf("%s: no emitted files on disk under %s", name, input)
			}
		})
	}
}
