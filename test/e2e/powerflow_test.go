// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package e2e_test

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"math"
	"path/filepath"
	"strconv"
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

// TestE2EPyPSAPowerFlowReactive: the reactive network on the real framework.
// The slack holds 1.02 pu, the PV generator holds its bus at 1.0 pu by
// supplying reactive power, the derived and set reactive loads apply, the
// shunt draws reactive power at mv1, the controlled link carries its 3 MW
// into the island that PyPSA balances on its own generator.
func TestE2EPyPSAPowerFlowReactive(t *testing.T) {
	requireE2E(t)
	t.Parallel()
	srv, _ := newE2EServer(t)
	sub := submit(t, srv.URL, model.TargetPyPSA, readScenario(t, "pypsa_power_flow_reactive.json"))
	id := sub["id"].(string)
	status := awaitJob(t, srv.URL, id)
	logText, _ := status["log"].(string)
	if status["state"] != string(service.StateSucceeded) {
		t.Fatalf("reactive power flow failed: %v\nlog:\n%s", status["error"], logText)
	}
	if !strings.Contains(logText, "power flow converged 4/4 snapshots") {
		t.Errorf("log lacks the convergence line:\n%s", logText)
	}
	files := fetchZip(t, srv.URL, id)
	s := pfSummary(t, files)
	if s["converged_snapshots"] != 4.0 || s["slack_bus"] != "hv::electricity" {
		t.Errorf("summary: %v", s)
	}
	col := func(file, name string) []float64 {
		t.Helper()
		raw, ok := files["files/run_0/output/power_flow/"+file]
		if !ok {
			t.Fatalf("bundle lacks power_flow/%s (have %v)", file, zipNames(files))
		}
		recs, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
		if err != nil || len(recs) < 2 {
			t.Fatalf("%s: %v", file, err)
		}
		idx := -1
		for i, h := range recs[0] {
			if h == name {
				idx = i
			}
		}
		if idx < 0 {
			t.Fatalf("%s has no column %q (header %v)", file, name, recs[0])
		}
		out := make([]float64, 0, len(recs)-1)
		for _, r := range recs[1:] {
			f, _ := strconv.ParseFloat(r[idx], 64)
			out = append(out, f)
		}
		return out
	}
	for _, v := range col("buses-v_mag_pu.csv", "hv::electricity") {
		if math.Abs(v-1.02) > 1e-3 {
			t.Errorf("slack bus should hold 1.02 pu, got %v", v)
		}
	}
	for _, v := range col("buses-v_mag_pu.csv", "mv2::electricity") {
		if math.Abs(v-1.0) > 1e-3 {
			t.Errorf("pv bus should be held at 1.0 pu, got %v", v)
		}
	}
	if q := col("generators-q.csv", "pv"); q[1] <= 0 {
		t.Errorf("the pv generator must supply reactive power to hold its voltage, got %v", q)
	}
	if q := col("shunt_impedances-q.csv", "mv1::shunt"); math.Abs(q[0]) < 0.1 {
		t.Errorf("the shunt at mv1 should draw reactive power, got %v", q)
	}
	if p := col("links-p0.csv", "hvdc"); math.Abs(p[0]-3) > 1e-6 {
		t.Errorf("the controlled link carries its dispatch, got %v", p)
	}
	if q := col("loads-q.csv", "load1"); math.Abs(q[1]-12*math.Tan(math.Acos(0.95))) > 1e-3 {
		t.Errorf("load1 reactive power derived from the power factor, got %v", q)
	}
}

// resultCol reads one column of a results/ table out of the bundle.
func resultCol(t *testing.T, files map[string][]byte, file, name string) []float64 {
	t.Helper()
	raw, ok := files["files/run_0/output/results/"+file]
	if !ok {
		t.Fatalf("bundle lacks results/%s (have %v)", file, zipNames(files))
	}
	recs, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil || len(recs) < 2 {
		t.Fatalf("%s: %v", file, err)
	}
	idx := -1
	for i, h := range recs[0] {
		if h == name {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("%s has no column %q (header %v)", file, name, recs[0])
	}
	out := make([]float64, 0, len(recs)-1)
	for _, r := range recs[1:] {
		f, _ := strconv.ParseFloat(r[idx], 64)
		out = append(out, f)
	}
	return out
}

// TestE2EPyPSALinearPowerFlow: the optimization under the voltage law on the
// real framework. The three line flows around the ring satisfy Kirchhoff's
// voltage law (the reactance-weighted sum around the loop is zero), the
// expandable line was built out, and the results folder carries the planning
// tables a consumer reads like the power-flow tables.
func TestE2EPyPSALinearPowerFlow(t *testing.T) {
	requireE2E(t)
	t.Parallel()
	srv, _ := newE2EServer(t)
	sub := submit(t, srv.URL, model.TargetPyPSA, readScenario(t, "pypsa_lopf.json"))
	id := sub["id"].(string)
	status := awaitJob(t, srv.URL, id)
	logText, _ := status["log"].(string)
	if status["state"] != string(service.StateSucceeded) {
		t.Fatalf("lopf failed: %v\nlog:\n%s", status["error"], logText)
	}
	if !strings.Contains(logText, "status ok optimal") {
		t.Errorf("log lacks the optimal status:\n%s", logText)
	}
	files := fetchZip(t, srv.URL, id)
	ab, bc, ac := resultCol(t, files, "lines-p0.csv", "ab"), resultCol(t, files, "lines-p0.csv", "bc"), resultCol(t, files, "lines-p0.csv", "ac")
	for i := range ab {
		if kvl := 10*ab[i] + 10*bc[i] - 20*ac[i]; math.Abs(kvl) > 1e-3 {
			t.Errorf("snapshot %d: Kirchhoff's voltage law violated around the ring: %g (flows %g %g %g)", i, kvl, ab[i], bc[i], ac[i])
		}
	}
	if len(ab) == 3 && math.Abs(ab[2]) <= 30*0.9+1e-6 && math.Abs(ab[2]) < 31 {
		t.Logf("line ab at the last snapshot: %g MW", ab[2])
	}
	for _, f := range []string{"generators-p.csv", "loads-p.csv", "buses-marginal_price.csv", "capacities.csv", "statistics.csv", "summary.json"} {
		if _, ok := files["files/run_0/output/results/"+f]; !ok {
			t.Errorf("results folder lacks %s (have %v)", f, zipNames(files))
		}
	}
	var summary map[string]any
	if err := json.Unmarshal(files["files/run_0/output/results/summary.json"], &summary); err != nil {
		t.Fatal(err)
	}
	if obj, _ := summary["objective"].(float64); obj <= 0 {
		t.Errorf("summary objective: %v", summary)
	}
	// The expandable line ends larger than its 30 MW start: the 80 MW peak at
	// b cannot be served through the ring otherwise without the 50 €/MWh peaker.
	caps := string(files["files/run_0/output/results/capacities.csv"])
	if !strings.Contains(caps, "Line,ab,") {
		t.Errorf("capacities.csv should list the line: %s", caps)
	}
}

// TestE2EPyPSASecurityConstrained: the same network must survive the outage
// of line bc; the solve is at least as expensive as the unconstrained one.
func TestE2EPyPSASecurityConstrained(t *testing.T) {
	requireE2E(t)
	t.Parallel()
	srv, _ := newE2EServer(t)
	plain := submit(t, srv.URL, model.TargetPyPSA, readScenario(t, "pypsa_lopf.json"))
	// Losing bc leaves ab (expandable) to feed b alone; losing ab instead would
	// leave only the two fixed 30 MW lines for an 80 MW peak — infeasible by
	// construction, which is the honest answer, not a test.
	payload := powerFlowPayloadFrom(t, "pypsa_lopf.json", func(j *model.Job) {
		j.Experiment.Network.Security = &model.SecurityOptions{Outages: []string{"bc"}}
	})
	secure := submit(t, srv.URL, model.TargetPyPSA, payload)
	st1 := awaitJob(t, srv.URL, plain["id"].(string))
	st2 := awaitJob(t, srv.URL, secure["id"].(string))
	if st1["state"] != string(service.StateSucceeded) || st2["state"] != string(service.StateSucceeded) {
		t.Fatalf("states: plain %v secure %v\nlog:\n%v", st1["state"], st2["state"], st2["log"])
	}
	o1, ok1 := objectiveFromLog(st1["log"].(string))
	o2, ok2 := objectiveFromLog(st2["log"].(string))
	if !ok1 || !ok2 {
		t.Fatalf("objectives not found in logs")
	}
	if o2 < o1-1e-6 {
		t.Errorf("a security-constrained solve cannot be cheaper: %g < %g", o2, o1)
	}
	if !strings.Contains(st2["log"].(string), "security-constrained") {
		t.Errorf("the log should say the solve was security-constrained:\n%v", st2["log"])
	}
}

// powerFlowPayloadFrom adapts any scenario payload.
func powerFlowPayloadFrom(t *testing.T, file string, mut func(j *model.Job)) []byte {
	t.Helper()
	var j model.Job
	if err := json.Unmarshal(readScenario(t, file), &j); err != nil {
		t.Fatal(err)
	}
	mut(&j)
	out, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestE2EPyPSAStore: the Store topology solves on the real framework; the
// sidecar constraints hold in the optimum (charger and discharger sized
// alike, each within the rate times the energy built), the Store results
// are reported, and the pinned state-of-charge profile is honored.
func TestE2EPyPSAStore(t *testing.T) {
	requireE2E(t)
	t.Parallel()
	srv, _ := newE2EServer(t)
	sub := submit(t, srv.URL, model.TargetPyPSA, readScenario(t, "pypsa_store.json"))
	status := awaitJob(t, srv.URL, sub["id"].(string))
	if status["state"] != string(service.StateSucceeded) {
		t.Fatalf("store scenario failed: %v\nlog:\n%v", status["error"], status["log"])
	}
	files := fetchZip(t, srv.URL, sub["id"].(string))
	caps := map[string]float64{}
	recs, err := csv.NewReader(bytes.NewReader(files["files/run_0/output/results/capacities.csv"])).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs[1:] {
		v, _ := strconv.ParseFloat(r[3], 64)
		caps[r[0]+":"+r[1]] = v
	}
	e, ch, dis := caps["Store:battery"], caps["Link:battery_charge"], caps["Link:battery_discharge"]
	if e <= 0 {
		t.Fatalf("the battery should be built: %v", caps)
	}
	if math.Abs(ch-dis) > 1e-6 {
		t.Errorf("charger and discharger must be sized alike: %g vs %g", ch, dis)
	}
	if ch > 0.5*e+1e-6 {
		t.Errorf("charger power %g exceeds half the energy %g", ch, e)
	}
	if soc := resultCol(t, files, "storage_units-state_of_charge.csv", "pumped"); len(soc) == 0 || math.Abs(soc[2]-12) > 1e-6 {
		t.Errorf("the pinned state of charge should hold at 12 MWh, got %v", soc)
	}
	if _, ok := files["files/run_0/output/results/stores-e.csv"]; !ok {
		t.Errorf("results lack stores-e.csv (have %v)", zipNames(files))
	}
}

// TestE2EPyPSAModularRetireSlackCopperplate: the three planning additions on
// the real framework. Modules: the built capacity is a whole number of 7 MW
// units. Retirement and slack: the old unit retires what the horizon never
// uses, the island's shortfall is served by the priced slack. Copperplate:
// a load on nodes without arcs is served through the hub.
func TestE2EPyPSAModularRetireSlackCopperplate(t *testing.T) {
	requireE2E(t)
	t.Parallel()
	srv, _ := newE2EServer(t)
	ids := map[string]string{}
	for _, f := range []string{"pypsa_modular.json", "pypsa_retire_slack.json", "pypsa_copperplate.json"} {
		ids[f] = submit(t, srv.URL, model.TargetPyPSA, readScenario(t, f))["id"].(string)
	}
	results := map[string]map[string][]byte{}
	for f, id := range ids {
		st := awaitJob(t, srv.URL, id)
		if st["state"] != string(service.StateSucceeded) {
			t.Fatalf("%s failed: %v\nlog:\n%v", f, st["error"], st["log"])
		}
		results[f] = fetchZip(t, srv.URL, id)
	}
	capsOf := func(files map[string][]byte) map[string]float64 {
		recs, err := csv.NewReader(bytes.NewReader(files["files/run_0/output/results/capacities.csv"])).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]float64{}
		for _, r := range recs[1:] {
			v, _ := strconv.ParseFloat(r[3], 64)
			out[r[0]+":"+r[1]] = v
		}
		return out
	}
	caps := capsOf(results["pypsa_modular.json"])
	if m := caps["Generator:modular"]; m <= 0 || math.Abs(math.Mod(m, 7)) > 1e-6 && math.Abs(math.Mod(m, 7)-7) > 1e-6 {
		t.Errorf("modular capacity should be a positive multiple of 7 MW, got %v", m)
	}
	if e := caps["Store:battery"]; e > 0 && math.Abs(math.Mod(e, 5)) > 1e-6 && math.Abs(math.Mod(e, 5)-5) > 1e-6 {
		t.Errorf("store energy should come in 5 MWh modules, got %v", e)
	}
	caps = capsOf(results["pypsa_retire_slack.json"])
	if old := caps["Generator:old"]; old >= 60-1e-6 || old < 40-1e-6 {
		t.Errorf("the old unit should retire what the horizon never uses (40 MW kept of 60), got %v", old)
	}
	if unmet := resultCol(t, results["pypsa_retire_slack.json"], "generators-p.csv", "b::electricity::unmet"); len(unmet) != 4 || unmet[3] < 20-1e-6 || unmet[0] > 1e-6 {
		t.Errorf("the island's shortfall should be served by the slack (20 MW in the last hour, none in the first), got %v", unmet)
	}
	if flow := resultCol(t, results["pypsa_copperplate.json"], "links-p0.csv", "n1::electricity::copperplate"); len(flow) != 3 || flow[2] < 80-1e-6 {
		t.Errorf("the copperplate hub should carry the generator's output to the two loads (80 MW in the last hour), got %v", flow)
	}
}

// TestE2EPyPSAUnitCommitmentQuadraticMultiPeriod: the last three planning
// additions on the real framework. The committable unit solves with its
// start/stop ramps, standby cost and prior uptime; the quadratic cost
// spreads the dispatch (a convex QP on HiGHS); the two-period plan retires
// the 2020 unit before 2040 and builds new capacity in that period only.
func TestE2EPyPSAUnitCommitmentQuadraticMultiPeriod(t *testing.T) {
	requireE2E(t)
	t.Parallel()
	srv, _ := newE2EServer(t)
	ids := map[string]string{}
	for _, f := range []string{"pypsa_uc_extras.json", "pypsa_quadratic.json", "pypsa_multi_period.json"} {
		ids[f] = submit(t, srv.URL, model.TargetPyPSA, readScenario(t, f))["id"].(string)
	}
	results := map[string]map[string][]byte{}
	for f, id := range ids {
		st := awaitJob(t, srv.URL, id)
		if st["state"] != string(service.StateSucceeded) {
			t.Fatalf("%s failed: %v\nlog:\n%v", f, st["error"], st["log"])
		}
		results[f] = fetchZip(t, srv.URL, id)
	}
	if p := resultCol(t, results["pypsa_uc_extras.json"], "generators-p.csv", "ccgt"); len(p) != 4 || p[1] <= 0 {
		t.Errorf("the committable unit should run: %v", p)
	}
	// With a quadratic cost the cheap unit no longer takes the whole load at
	// the 80 MW hours: marginal 10 + 0.5 x 2p meets the peaker's 60 at 50 MW.
	if p := resultCol(t, results["pypsa_quadratic.json"], "generators-p.csv", "quad"); len(p) != 4 || math.Abs(p[1]-50) > 0.5 {
		t.Errorf("quadratic cost should cap the cheap unit near 50 MW at the peak, got %v", p)
	}
	recs, err := csv.NewReader(bytes.NewReader(results["pypsa_multi_period.json"]["files/run_0/output/results/capacities.csv"])).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	caps := map[string]float64{}
	for _, r := range recs[1:] {
		v, _ := strconv.ParseFloat(r[3], 64)
		caps[r[1]] = v
	}
	if caps["new-2030"] > 1e-6 || caps["new-2040"] < 40-1e-6 {
		t.Errorf("the 2020 unit covers 2030 and retires before 2040, so new capacity is built in 2040 only: %v", caps)
	}
}
