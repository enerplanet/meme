// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package e2e_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/service"
)

// powerFlowPayload returns the power-flow scenario, optionally adapted.
func powerFlowPayload(t *testing.T, mut func(j *model.Job)) []byte {
	t.Helper()
	raw := readScenario(t, "pypsa_power_flow.json")
	if mut == nil {
		return raw
	}
	var j model.Job
	if err := json.Unmarshal(raw, &j); err != nil {
		t.Fatal(err)
	}
	mut(&j)
	out, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// pfSummary decodes power_flow/summary.json out of the result bundle.
func pfSummary(t *testing.T, files map[string][]byte) map[string]any {
	t.Helper()
	raw, ok := files["files/run_0/output/power_flow/summary.json"]
	if !ok {
		t.Fatalf("bundle lacks power_flow/summary.json (have %v)", zipNames(files))
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("summary.json: %v", err)
	}
	return s
}

// TestE2EPyPSAPowerFlow runs the AC network through PyPSA's Newton-Raphson
// power flow: every snapshot converges, the run exits 0, the electrical
// results land on disk and in the bundle with plausible magnitudes (the HV
// slack holds 1.0 pu, the MV buses sag under load, the line is loaded).
func TestE2EPyPSAPowerFlow(t *testing.T) {
	requireE2E(t)
	t.Parallel()
	srv, work := newE2EServer(t)
	sub := submit(t, srv.URL, model.TargetPyPSA, powerFlowPayload(t, nil))
	id := sub["id"].(string)
	status := awaitJob(t, srv.URL, id)
	logText, _ := status["log"].(string)
	if status["state"] != string(service.StateSucceeded) {
		t.Fatalf("power flow failed: %v\nlog:\n%s", status["error"], logText)
	}
	for _, m := range []string{"power flow converged 4/4 snapshots", "status ok power flow converged"} {
		if !strings.Contains(logText, m) {
			t.Errorf("log lacks %q:\n%s", m, logText)
		}
	}
	if strings.Contains(logText, "objective") {
		t.Error("a power flow has no objective; the driver must not print one")
	}

	jobDir := jobDirOf(t, work)
	mustExist(t,
		filepath.Join(jobDir, "run_0", "input", "lines.csv"),
		filepath.Join(jobDir, "run_0", "input", "transformers.csv"),
		filepath.Join(jobDir, "run_0", "input", "generators-p_set.csv"),
		filepath.Join(jobDir, "run_0", "output", "network.nc"),
		filepath.Join(jobDir, "run_0", "output", "power_flow", "convergence.csv"),
		filepath.Join(jobDir, "run_0", "output", "power_flow", "buses-v_mag_pu.csv"),
		filepath.Join(jobDir, "run_0", "output", "power_flow", "lines-loading.csv"),
		filepath.Join(jobDir, "run_0", "output", "power_flow", "transformers-loading.csv"),
		filepath.Join(jobDir, "run_0", "output", "power_flow", "generators-p.csv"),
		filepath.Join(jobDir, "run_0", "output", "power_flow", "summary.json"),
	)

	files := fetchZip(t, srv.URL, id)
	s := pfSummary(t, files)
	if s["method"] != "newton_raphson" || s["snapshots"] != 4.0 || s["converged_snapshots"] != 4.0 {
		t.Errorf("summary: %v", s)
	}
	minV, maxV := s["min_v_mag_pu"].(float64), s["max_v_mag_pu"].(float64)
	if minV < 0.9 || minV >= 1.0 || maxV < 1.0 || maxV > 1.05 {
		t.Errorf("voltage magnitudes out of the plausible band: min %v max %v", minV, maxV)
	}
	if ll, _ := s["max_line_loading"].(float64); ll <= 0 || ll > 1 {
		t.Errorf("line loading should be a positive fraction below 1 for this network, got %v", ll)
	}
	if tl, _ := s["max_transformer_loading"].(float64); tl <= 0 {
		t.Errorf("transformer loading should be positive, got %v", tl)
	}
	if s["slack_bus"] != "hv::electricity" {
		t.Errorf("slack bus: %v", s["slack_bus"])
	}
	conv := string(files["files/run_0/output/power_flow/convergence.csv"])
	if strings.Count(conv, "\n") < 4 || !strings.Contains(conv, "True") {
		t.Errorf("convergence.csv should list four converged snapshots:\n%s", conv)
	}
	vmag := string(files["files/run_0/output/power_flow/buses-v_mag_pu.csv"])
	if !strings.Contains(vmag, "hv::electricity") || !strings.Contains(vmag, "mv2::electricity") {
		t.Errorf("buses-v_mag_pu.csv should have one column per AC bus:\n%s", vmag)
	}
}

// TestE2EPyPSAPowerFlowLinear: the linear (DC) method solves the same network
// without iterating; voltages stay nominal and no reactive power is reported.
func TestE2EPyPSAPowerFlowLinear(t *testing.T) {
	requireE2E(t)
	t.Parallel()
	srv, _ := newE2EServer(t)
	payload := powerFlowPayload(t, func(j *model.Job) { j.Experiment.PowerFlow.Method = model.PowerFlowLinear })
	sub := submit(t, srv.URL, model.TargetPyPSA, payload)
	id := sub["id"].(string)
	status := awaitJob(t, srv.URL, id)
	if status["state"] != string(service.StateSucceeded) {
		t.Fatalf("linear power flow failed: %v\nlog:\n%v", status["error"], status["log"])
	}
	files := fetchZip(t, srv.URL, id)
	s := pfSummary(t, files)
	if s["method"] != "linear" || s["converged_snapshots"] != 4.0 {
		t.Errorf("summary: %v", s)
	}
	if s["min_v_mag_pu"] != 1.0 || s["max_v_mag_pu"] != 1.0 {
		t.Errorf("a linear power flow keeps nominal voltage, got %v..%v", s["min_v_mag_pu"], s["max_v_mag_pu"])
	}
	if _, ok := files["files/run_0/output/power_flow/buses-q.csv"]; ok {
		t.Error("a linear power flow reports no reactive power")
	}
	if _, ok := files["files/run_0/output/power_flow/lines-p0.csv"]; !ok {
		t.Error("line flows must still be reported")
	}
}

// TestE2EPyPSAPowerFlowDiverges: a load far beyond what the line can carry
// has no power-flow solution. The run must fail loudly (exit 3, job failed)
// while still writing the convergence table for diagnosis.
func TestE2EPyPSAPowerFlowDiverges(t *testing.T) {
	requireE2E(t)
	t.Parallel()
	srv, work := newE2EServer(t)
	payload := powerFlowPayload(t, func(j *model.Job) {
		j.Model.Timeseries["dem2"] = model.TimeSeries{Source: "inline", Values: []float64{900, 900, 900, 900}}
	})
	sub := submit(t, srv.URL, model.TargetPyPSA, payload)
	status := awaitJob(t, srv.URL, sub["id"].(string))
	logText, _ := status["log"].(string)
	if status["state"] != string(service.StateFailed) {
		t.Fatalf("a diverging power flow must fail the job, got state %v\nlog:\n%s", status["state"], logText)
	}
	for _, run := range runsOf(t, status) {
		if ec, _ := run["exit_code"].(float64); ec != 3 {
			t.Errorf("run exit_code = %v, want 3", ec)
		}
	}
	if !strings.Contains(logText, "power flow did not converge") || !strings.Contains(logText, "status failed") {
		t.Errorf("log should say why the run failed:\n%s", logText)
	}
	jobDir := jobDirOf(t, work)
	mustExist(t, filepath.Join(jobDir, "run_0", "output", "power_flow", "convergence.csv"))
}
