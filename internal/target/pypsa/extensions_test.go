// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target"
	"github.com/enerplanet/meme/internal/target/pypsa"
)

func fp(v float64) *float64 { return &v }

// planRunPy runs Plan into a temp dir and returns run.py's contents (the CFG
// line carries the run configuration).
func planRunPy(t *testing.T, j model.Job) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Plan(&j, "input", target.RunDirs{RunDir: dir}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	return readFile(t, dir, "run.py")
}

// First-class solver fields land in CFG mapped to the solver's option names;
// first-class alternatives/operate blocks beat the legacy options namespaces.
func TestRunPyTypedConfig(t *testing.T) {
	j := loadSample(t)
	j.Experiment.Mode = model.ModeAlternatives
	j.Experiment.Alternatives = &model.AlternativesOptions{Slack: fp(0.1)}
	j.Experiment.Operate = &model.OperateOptions{Horizon: "48h"}
	j.Experiment.Solver = model.Solver{Name: "gurobi", TimeLimit: fp(7200), Threads: intp(4),
		Options: map[string]any{"mga": map[string]any{"slack": 0.9}}} // legacy loses
	py := planRunPy(t, j)
	for _, want := range []string{
		`"mga_slack":0.1`,
		`"horizon":48`,
		`"solver_options":{"Threads":4,"TimeLimit":7200}`,
	} {
		if !strings.Contains(py, want) {
			t.Errorf("run.py CFG missing %q:\n%s", want, py[:200])
		}
	}
	// The driver must consume the options.
	if !strings.Contains(py, "**SOLVER_OPTS") {
		t.Error("run.py does not forward SOLVER_OPTS to the solver")
	}
}

func intp(v int) *int { return &v }

// No solver fields -> no solver_options key in the CFG line (byte-identical
// legacy configuration; the script body may mention the key).
func TestRunPyNoSolverOptions(t *testing.T) {
	j := loadSample(t)
	py := planRunPy(t, j)
	cfgLine, _, _ := strings.Cut(py, "\n")
	if strings.Contains(cfgLine, "solver_options") {
		t.Errorf("unexpected solver_options in CFG line:\n%s", cfgLine)
	}
}

// A series-valued trade limit becomes a fixed p_nom=1 generator whose absolute
// bound is materialized as time-varying p_max_pu (import) / negated p_min_pu
// (export).
func TestTradeSeriesLimit(t *testing.T) {
	j := loadSample(t)
	j.Model.Timeseries["grid_cap"] = model.TimeSeries{Source: "inline", Values: []float64{100, 50, 0}}
	tr := j.Model.Trade["grid_n1"]
	tr.Import.Limit = model.Series("grid_cap")
	tr.Export.Limit = model.Series("grid_cap")
	j.Model.Trade["grid_n1"] = tr
	if _, err := validateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	gens := readFile(t, dir, "generators.csv")
	for _, want := range []string{"grid_n1_import,n1::electricity,electricity,1,False", "grid_n1_export,n1::electricity,electricity,1,False"} {
		if !strings.Contains(gens, want) {
			t.Errorf("generators.csv missing fixed-p_nom trade row %q:\n%s", want, gens)
		}
	}
	pmax := readFile(t, dir, "generators-p_max_pu.csv")
	if !strings.Contains(pmax, "grid_n1_import") || !strings.Contains(pmax, "100") {
		t.Errorf("import limit series not materialized:\n%s", pmax)
	}
	pmin := readFile(t, dir, "generators-p_min_pu.csv")
	if !strings.Contains(pmin, "grid_n1_export") || !strings.Contains(pmin, "-100") {
		t.Errorf("export limit series not negated into p_min_pu:\n%s", pmin)
	}
}

// Per-distance losses fold into the link efficiency; a one-way link may carry
// a minimum-flow floor.
func TestTransmissionLossAndMinFlow(t *testing.T) {
	j := loadSample(t)
	l := j.Model.Transmission["line_n1_n2"]
	l.Bidirectional = false
	l.Distance = fp(100)
	l.LossPerDistance = fp(0.0001)
	l.MinFlow = fp(0.05)
	j.Model.Transmission["line_n1_n2"] = l
	if _, err := validateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	links := readFile(t, dir, "links.csv")
	// 0.97 * (1 - 0.0001*100) = 0.9603
	if !strings.Contains(links, "0.9603") {
		t.Errorf("loss not folded into efficiency:\n%s", links)
	}
	if !strings.Contains(links, "0.05") {
		t.Errorf("min_flow not emitted as p_min_pu:\n%s", links)
	}
}

func TestTransmissionExtensionGates(t *testing.T) {
	// loss_per_distance without a distance cannot fold.
	j := loadSample(t)
	l := j.Model.Transmission["line_n1_n2"]
	l.LossPerDistance = fp(0.0001)
	j.Model.Transmission["line_n1_n2"] = l
	if _, err := validateFor(&j, model.TargetPyPSA); err == nil ||
		!strings.Contains(err.Error(), "needs distance") {
		t.Fatalf("want loss-without-distance rejection, got: %v", err)
	}

	// min_flow on a bidirectional link conflicts with p_min_pu = -1.
	j = loadSample(t)
	l = j.Model.Transmission["line_n1_n2"]
	l.MinFlow = fp(0.05)
	j.Model.Transmission["line_n1_n2"] = l
	if _, err := validateFor(&j, model.TargetPyPSA); err == nil ||
		!strings.Contains(err.Error(), "bidirectional") {
		t.Fatalf("want bidirectional min_flow rejection, got: %v", err)
	}
}

func TestStorageExtensionsRejected(t *testing.T) {
	for _, mut := range []func(*model.Storage){
		func(s *model.Storage) { s.MaxHours = nil; s.MaxChargeRate = fp(0.25) },
		func(s *model.Storage) { s.DepthOfDischarge = fp(0.1) },
		func(s *model.Storage) { s.NoSimultaneousChargeDischarge = true },
	} {
		j := loadSample(t)
		b := j.Model.Technologies["battery"]
		mut(b.Storage)
		j.Model.Technologies["battery"] = b
		if _, err := validateFor(&j, model.TargetPyPSA); err == nil ||
			!strings.Contains(err.Error(), "not representable") {
			t.Fatalf("want storage-extension rejection, got: %v", err)
		}
	}
}

// --- second tranche -----------------------------------------------------------

// e_sum bounds, build_year, active and equals_pu land as generator columns;
// systemwide bounds synthesize sidecar constraints; weights fill snapshots.csv.
func TestPyPSASecondTranche(t *testing.T) {
	j := loadSample(t)
	pv := j.Model.Technologies["pv"]
	pv.AnnualOutputMin = fp(100)
	pv.AnnualOutputMax = fp(5000)
	pv.BuildYear = intp(2024)
	pv.Capacity.SystemwideMax = fp(700)
	j.Model.Technologies["pv"] = pv
	ccgt := j.Model.Technologies["ccgt"]
	no := false
	ccgt.Active = &no
	j.Model.Technologies["ccgt"] = ccgt
	j.Model.Time.Weights = model.Num(3)
	if _, err := validateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	gens := readFile(t, dir, "generators.csv")
	for _, want := range []string{"e_sum_min", "e_sum_max", "build_year", "2024", "active", "False"} {
		if !strings.Contains(gens, want) {
			t.Errorf("generators.csv missing %q:\n%s", want, gens)
		}
	}
	cons := readFile(t, dir, "_constraints.json")
	if !strings.Contains(cons, "pv_systemwide_max") || !strings.Contains(cons, "700") {
		t.Errorf("systemwide sidecar constraint missing:\n%s", cons)
	}
	snaps := readFile(t, dir, "snapshots.csv")
	if !strings.Contains(snaps, "objective") || !strings.Contains(snaps, "3") {
		t.Errorf("snapshot weightings missing:\n%s", snaps)
	}
}

// Scalar fuel cost folds into marginal_cost as fuel/efficiency; the fraction
// O&M folds into capital_cost.
func TestPyPSAFuelAndFraction(t *testing.T) {
	j := loadSample(t)
	ccgt := j.Model.Technologies["ccgt"]
	ccgt.Efficiency = model.Num(0.5)
	ccgt.Costs = map[string]model.CostClass{"monetary": {
		InvestmentPerCapacity: fp(1000), FuelCost: model.Num(20), VariableOM: model.Num(2),
	}}
	ccgt.CostBasis = model.CostAnnualized
	j.Model.Technologies["ccgt"] = ccgt
	if _, err := validateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	// marginal = 2 + 20/0.5 = 42
	if gens := readFile(t, dir, "generators.csv"); !strings.Contains(gens, "42") {
		t.Errorf("fuel cost not folded into marginal_cost:\n%s", gens)
	}
}

func TestPyPSASecondTrancheGates(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*model.Job)
		want string
	}{
		{"tech emission factor", func(j *model.Job) {
			pv := j.Model.Technologies["pv"]
			pv.EmissionFactor = fp(0.1)
			j.Model.Technologies["pv"] = pv
		}, "emission_factor"},
		{"decommission continuous", func(j *model.Job) {
			pv := j.Model.Technologies["pv"]
			pv.Capacity.Decommission = &model.Decommission{Mode: "continuous"}
			j.Model.Technologies["pv"] = pv
		}, "decommission"},
		{"copperplate", func(j *model.Job) { j.Experiment.Copperplate = true }, "copperplate"},
		{"unmet demand", func(j *model.Job) {
			j.Experiment.AllowUnmetDemand = &model.AllowUnmetDemand{Enabled: true, PenaltyPrice: fp(100)}
		}, "allow_unmet_demand"},
		{"positive-only accounting", func(j *model.Job) {
			j.Experiment.EmissionAccounting = "positive_only"
		}, "emission_accounting"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := loadSample(t)
			tc.mut(&j)
			if _, err := validateFor(&j, model.TargetPyPSA); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want rejection containing %q, got: %v", tc.want, err)
			}
		})
	}
}

// Auxiliary transport energy becomes a second link port withdrawing the
// consumed carrier (negative efficiency2), sized by per_flow + per_flow_distance.
func TestTransmissionEnergyConsumption(t *testing.T) {
	j := loadSample(t)
	l := j.Model.Transmission["line_n1_n2"]
	l.Bidirectional = false
	l.Distance = fp(100)
	l.EnergyConsumption = &model.TransportEnergy{Carrier: "electricity", PerFlow: 0.01, PerFlowDistance: 0.0001}
	j.Model.Transmission["line_n1_n2"] = l
	if _, err := validateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	links := readFile(t, dir, "links.csv")
	// rate = 0.01 + 0.0001*100 = 0.02 withdrawn per unit of flow
	if !strings.Contains(links, "-0.02") {
		t.Errorf("aux consumption port missing:\n%s", links)
	}

	// Bidirectional arcs cannot carry the aux port.
	j = loadSample(t)
	l = j.Model.Transmission["line_n1_n2"]
	l.EnergyConsumption = &model.TransportEnergy{Carrier: "electricity", PerFlow: 0.01}
	j.Model.Transmission["line_n1_n2"] = l
	if _, err := validateFor(&j, model.TargetPyPSA); err == nil ||
		!strings.Contains(err.Error(), "bidirectional") {
		t.Fatalf("want bidirectional energy_consumption rejection, got: %v", err)
	}
}

// Explicit timestep labels drive the emitted snapshot index.
func TestExplicitTimesteps(t *testing.T) {
	j := loadSample(t)
	j.Model.Time.Timesteps = []string{"2030-01-01 00:00", "2030-01-01 06:00", "2030-01-01 12:00"}
	j.Model.Time.Weights = model.Num(6)
	if _, err := validateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	if _, err := (pypsa.PyPSA{}).Emit(&j, dir); err != nil {
		t.Fatalf("emit: %v", err)
	}
	snaps := readFile(t, dir, "snapshots.csv")
	if !strings.Contains(snaps, "2030-01-01 06:00") {
		t.Errorf("explicit timestep labels not used:\n%s", snaps)
	}
}

// String-valued overrides have no PyPSA attribute translation in stochastic mode.
func TestStochasticNonNumericOverrideRejected(t *testing.T) {
	j := loadSample(t)
	j.Experiment.Mode = model.ModeStochastic
	j.Experiment.Scenarios = []model.Scenario{
		{Name: "s1", Weight: 1, Overrides: map[string]any{"technologies.pv.capacity.max": "big"}},
	}
	// Stochastic + the sample's constraints are rejected first; drop them.
	j.Model.Constraints = nil
	if _, err := validateFor(&j, model.TargetPyPSA); err == nil ||
		!strings.Contains(err.Error(), "non-numeric") {
		t.Fatalf("want non-numeric override rejection, got: %v", err)
	}
}

// reporting.output_dir is service-managed and must warn, not silently vanish.
func TestReportingOutputDirWarns(t *testing.T) {
	j := loadSample(t)
	j.Experiment.Reporting = &model.Reporting{OutputDir: "/tmp/out"}
	warns, err := validateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "output_dir") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an output_dir warning, got %v", warns)
	}
}
