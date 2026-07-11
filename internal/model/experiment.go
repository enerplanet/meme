// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

import (
	"errors"
	"fmt"
	"sort"
)

// Solver settings are a run concern, so they live on the Experiment, not the
// physical Model. TimeLimit/MIPGap/Threads are the three options every run
// tunes; emitters map them onto each target's convention (AdOpT-NET0
// solveroptions in hours, linopy/pyomo solver options otherwise). Options
// remains the free-form passthrough for anything else.
type Solver struct {
	Name      string         `json:"name"`
	TimeLimit *float64       `json:"time_limit,omitempty"` // wall-clock limit, SECONDS
	MIPGap    *float64       `json:"mip_gap,omitempty"`    // relative MIP optimality gap
	Threads   *int           `json:"threads,omitempty"`    // 0/absent = solver default
	Options   map[string]any `json:"options,omitempty"`
}

// OperateOptions configures mode "operate" (receding-horizon dispatch).
// Window/Horizon are pandas frequency strings ("12h", "24h"); the PyPSA runner
// consumes the horizon as whole hours.
type OperateOptions struct {
	Window  string `json:"window,omitempty"`
	Horizon string `json:"horizon,omitempty"`
}

// AlternativesOptions configures mode "alternatives" (near-optimal
// alternatives: Calliope SPORES, PyPSA MGA).
type AlternativesOptions struct {
	Number           int      `json:"number,omitempty"`            // iterations after the base run
	Slack            *float64 `json:"slack,omitempty"`             // allowed cost slack over the optimum (fraction)
	ScoringAlgorithm string   `json:"scoring_algorithm,omitempty"` // SPORES scoring: integer | relative_deployment | random | evolving_average
}

// ParetoOptions configures mode "pareto" (cost/emission front; AdOpT-NET0).
type ParetoOptions struct {
	Points int `json:"points,omitempty"` // number of Pareto points
}

// MonteCarloOptions configures mode "monte_carlo" (parametric sampling;
// AdOpT-NET0). On selects the parameter classes to vary.
type MonteCarloOptions struct {
	Samples           int      `json:"samples,omitempty"`
	StandardDeviation *float64 `json:"standard_deviation,omitempty"` // relative sd of the sampled parameters
	On                []string `json:"on,omitempty"`                 // technology_capex | network_capex | import_price | export_price
}

// monteCarloOn maps the canonical parameter classes onto AdOpT-NET0's
// monte_carlo.on_what names.
var monteCarloOn = map[string]string{
	"technology_capex": "Technologies",
	"network_capex":    "Networks",
	"import_price":     "Import",
	"export_price":     "Export",
}

// scoringAlgorithms are the valid AlternativesOptions.ScoringAlgorithm values
// (Calliope config.solve.spores.scoring_algorithm).
var scoringAlgorithms = map[string]bool{
	"integer": true, "relative_deployment": true, "random": true, "evolving_average": true,
}

// Mode is the kind of run to perform. Each maps either to a target's native run
// configuration (operate/alternatives -> Calliope, pareto/monte_carlo -> AdOpT,
// stochastic -> PyPSA) or to orchestration the service performs itself (a sweep
// is N plan runs). Capability support is declared in capability.go.
type Mode string

const (
	ModePlan         Mode = "plan"         // single cost-optimal capacity+dispatch
	ModeOperate      Mode = "operate"      // fixed capacities, receding-horizon dispatch
	ModeAlternatives Mode = "alternatives" // near-optimal alternatives (SPORES/MGA)
	ModePareto       Mode = "pareto"       // multi-objective front
	ModeMonteCarlo   Mode = "monte_carlo"  // parametric uncertainty sampling
	ModeStochastic   Mode = "stochastic"   // scenario-weighted stochastic program
)

// Valid reports whether m is a defined run mode or unset (= plan).
func (m Mode) Valid() bool {
	switch m {
	case "", ModePlan, ModeOperate, ModeAlternatives, ModePareto, ModeMonteCarlo, ModeStochastic:
		return true
	}
	return false
}

// TimeAggregation requests temporal complexity reduction before solving.
type TimeAggregation struct {
	Method     string `json:"method"`               // "typical_days" | "resample" | "cluster"
	Periods    int    `json:"periods,omitempty"`    // e.g. number of typical days
	Resolution string `json:"resolution,omitempty"` // e.g. "3H" for resample
	// ClusterSeries names a series mapping each date to its representative
	// date (method "cluster"; no target claims it yet).
	ClusterSeries string `json:"cluster_series,omitempty"`
	// KeepFullResolutionFor lists technology ids or classes kept at full
	// temporal resolution during typical-day clustering (should include all
	// storage; AdOpT typicaldays.technologies_with_full_res).
	KeepFullResolutionFor []string `json:"keep_full_resolution_for,omitempty"`
}

// Scenario is one branch of a stochastic run (or one member of an ensemble),
// with a probability weight and parameter overrides applied to the base model.
// Override values are numbers for everything the stochastic runner consumes;
// strings/booleans are accepted for deterministic what-if expansion.
type Scenario struct {
	Name      string         `json:"name"`
	Weight    float64        `json:"weight,omitempty"`
	Overrides map[string]any `json:"overrides,omitempty"` // dotted param path -> value
}

// NumericOverride reads one override as a float64 (JSON numbers decode as
// float64; ints are tolerated for programmatic construction).
func (s *Scenario) NumericOverride(path string) (float64, bool) {
	switch v := s.Overrides[path].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	}
	return 0, false
}

// AllowUnmetDemand lets the energy balance be violated at a penalty price
// instead of making the model infeasible (AdOpT energybalance.violation;
// Calliope ensure_feasibility with a bigM penalty; rejected by PyPSA).
type AllowUnmetDemand struct {
	Enabled      bool     `json:"enabled,omitempty"`
	PenaltyPrice *float64 `json:"penalty_price,omitempty"` // cost per MWh of violated balance
}

// Reporting configures result reporting where a target has native hooks.
// Output locations are managed by the service layer; OutputDir is accepted
// for schema parity but ignored with a warning.
type Reporting struct {
	OutputDir    string   `json:"output_dir,omitempty"`
	CaseName     string   `json:"case_name,omitempty"`     // AdOpT reporting.case_name
	SaveLogs     string   `json:"save_logs,omitempty"`     // Calliope config.solve.save_logs
	ShadowPrices []string `json:"shadow_prices,omitempty"` // Calliope config.solve.shadow_prices
}

// SweepAxis is one dimension of a parameter sweep the orchestrator expands into
// multiple independent runs.
type SweepAxis struct {
	Parameter string    `json:"parameter"` // dotted path into the model
	Values    []float64 `json:"values"`
}

// Experiment is the run specification: everything about *how* to solve, kept
// separate from *what* the physical system is (Model). The typed per-mode
// blocks (Operate/Alternatives/Pareto/MonteCarlo) replace the legacy practice
// of tunnelling those settings through Solver.Options namespaces; emitters
// still honor the legacy keys (first-class fields win) and ValidateFor warns
// about them.
type Experiment struct {
	Mode      Mode      `json:"mode,omitempty"`      // default: plan
	Objective Objective `json:"objective,omitempty"` // default: min_cost
	// EmissionAccounting selects whether emission objectives/caps count net
	// emissions (default) or positive emissions only (AdOpT emissions_pos;
	// rejected elsewhere, where accounting is net by construction).
	EmissionAccounting string               `json:"emission_accounting,omitempty"` // "" | "net" | "positive_only"
	Foresight          Foresight            `json:"foresight,omitempty"`
	TimeAggregation    *TimeAggregation     `json:"time_aggregation,omitempty"`
	Operate            *OperateOptions      `json:"operate,omitempty"`
	Alternatives       *AlternativesOptions `json:"alternatives,omitempty"`
	Pareto             *ParetoOptions       `json:"pareto,omitempty"`
	MonteCarlo         *MonteCarloOptions   `json:"monte_carlo,omitempty"`
	AllowUnmetDemand   *AllowUnmetDemand    `json:"allow_unmet_demand,omitempty"`
	// Copperplate drops all inter-node transport constraints (AdOpT
	// energybalance.copperplate; rejected elsewhere).
	Copperplate bool        `json:"copperplate,omitempty"`
	Reporting   *Reporting  `json:"reporting,omitempty"`
	Scenarios   []Scenario  `json:"scenarios,omitempty"`
	Sweep       []SweepAxis `json:"sweep,omitempty"`
	Solver      Solver      `json:"solver"`
	// CustomMath (Calliope only) controls whether portable `constraints` and
	// `emission_limits` are rendered into a Calliope 0.7 add_math file. Nil or
	// true renders them (default); false leaves them in the JSON sidecar.
	CustomMath *bool   `json:"custom_math,omitempty"`
	Native     *Native `json:"native,omitempty"`
}

// Validate checks the experiment's enum values and the ranges of the typed
// mode-option blocks and solver fields. Mode/feature support per target is
// gated separately (target.ValidateFor).
func (e *Experiment) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if !e.Mode.Valid() {
		add("experiment.mode %q is invalid", e.Mode)
	}
	if !e.Objective.Valid() {
		add("experiment.objective %q is invalid", e.Objective)
	}
	if !e.Foresight.Valid() {
		add("experiment.foresight %q is invalid", e.Foresight)
	}
	switch e.EmissionAccounting {
	case "", "net", "positive_only":
	default:
		add("experiment.emission_accounting %q is invalid (net or positive_only)", e.EmissionAccounting)
	}
	if ta := e.TimeAggregation; ta != nil {
		switch ta.Method {
		case "typical_days", "resample":
		case "cluster":
			if ta.ClusterSeries == "" {
				add("experiment.time_aggregation method \"cluster\" needs cluster_series")
			}
		default:
			add("experiment.time_aggregation.method %q is invalid (typical_days, resample or cluster)", ta.Method)
		}
		if ta.Periods < 0 {
			add("experiment.time_aggregation.periods must not be negative")
		}
	}
	if ud := e.AllowUnmetDemand; ud != nil && ud.PenaltyPrice != nil && *ud.PenaltyPrice < 0 {
		add("experiment.allow_unmet_demand.penalty_price must not be negative")
	}
	if a := e.Alternatives; a != nil {
		if a.Number < 0 {
			add("experiment.alternatives.number must not be negative")
		}
		if a.Slack != nil && *a.Slack < 0 {
			add("experiment.alternatives.slack must not be negative")
		}
		if a.ScoringAlgorithm != "" && !scoringAlgorithms[a.ScoringAlgorithm] {
			add("experiment.alternatives.scoring_algorithm %q is invalid (integer, relative_deployment, random or evolving_average)", a.ScoringAlgorithm)
		}
	}
	if p := e.Pareto; p != nil && p.Points < 0 {
		add("experiment.pareto.points must not be negative")
	}
	if mc := e.MonteCarlo; mc != nil {
		if mc.Samples < 0 {
			add("experiment.monte_carlo.samples must not be negative")
		}
		if mc.StandardDeviation != nil && *mc.StandardDeviation <= 0 {
			add("experiment.monte_carlo.standard_deviation must be positive")
		}
		for _, on := range mc.On {
			if _, ok := monteCarloOn[on]; !ok {
				add("experiment.monte_carlo.on entry %q is invalid (technology_capex, network_capex, import_price or export_price)", on)
			}
		}
	}
	if s := &e.Solver; true {
		if s.TimeLimit != nil && *s.TimeLimit <= 0 {
			add("experiment.solver.time_limit must be positive (seconds)")
		}
		if s.MIPGap != nil && *s.MIPGap < 0 {
			add("experiment.solver.mip_gap must not be negative")
		}
		if s.Threads != nil && *s.Threads < 0 {
			add("experiment.solver.threads must not be negative")
		}
	}
	for i, sc := range e.Scenarios {
		if sc.Name == "" {
			add("experiment.scenarios[%d] has no name", i)
		}
		if sc.Weight < 0 {
			add("experiment.scenarios[%d] %q has a negative weight", i, sc.Name)
		}
	}
	for i, ax := range e.Sweep {
		if ax.Parameter == "" {
			add("experiment.sweep[%d] has no parameter path", i)
		}
		if len(ax.Values) == 0 {
			add("experiment.sweep[%d] (%s) has no values", i, ax.Parameter)
		}
	}
	return errors.Join(errs...)
}

// legacyOptionNamespaces are the Solver.Options keys that predate the typed
// Experiment blocks; ValidateFor warns when they are still used.
var legacyOptionNamespaces = map[string]string{
	"operate":     "experiment.operate",
	"spores":      "experiment.alternatives",
	"mga":         "experiment.alternatives",
	"pareto":      "experiment.pareto",
	"monte_carlo": "experiment.monte_carlo",
}

// OptionWarnings reports deprecated Solver.Options namespaces and typed
// mode-option blocks that do not match the effective mode (both are honored
// but usually indicate a mistake).
func (e *Experiment) OptionWarnings() []string {
	var w []string
	for _, key := range sortedStrKeys(e.Solver.Options) {
		if repl, ok := legacyOptionNamespaces[key]; ok {
			w = append(w, fmt.Sprintf("experiment.solver.options.%s is deprecated; use %s (first-class fields win when both are set)", key, repl))
		}
	}
	mode := e.EffectiveMode()
	blocks := []struct {
		set  bool
		name string
		mode Mode
	}{
		{e.Operate != nil, "operate", ModeOperate},
		{e.Alternatives != nil, "alternatives", ModeAlternatives},
		{e.Pareto != nil, "pareto", ModePareto},
		{e.MonteCarlo != nil, "monte_carlo", ModeMonteCarlo},
	}
	for _, b := range blocks {
		if b.set && mode != b.mode {
			w = append(w, fmt.Sprintf("experiment.%s is set but mode is %q; the block only takes effect in mode %q", b.name, mode, b.mode))
		}
	}
	return w
}

func sortedStrKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// customMathEnabled reports whether portable constraints should be emitted as
// Calliope add_math (default on).
func (e *Experiment) CustomMathEnabled() bool {
	return e.CustomMath == nil || *e.CustomMath
}

// EffectiveMode returns the mode with the default (plan) applied.
func (e *Experiment) EffectiveMode() Mode {
	if e.Mode == "" {
		return ModePlan
	}
	return e.Mode
}

// ExpandSweep returns the cartesian product of the sweep axes as one
// {parameter: value} assignment per run the orchestrator must launch. Empty
// sweep -> nil (a single base run).
func (e *Experiment) ExpandSweep() []map[string]float64 {
	if len(e.Sweep) == 0 {
		return nil
	}
	combos := []map[string]float64{{}}
	for _, axis := range e.Sweep {
		var next []map[string]float64
		for _, base := range combos {
			for _, v := range axis.Values {
				m := make(map[string]float64, len(base)+1)
				for k, val := range base {
					m[k] = val
				}
				m[axis.Parameter] = v
				next = append(next, m)
			}
		}
		combos = next
	}
	return combos
}

// Job is the full REST payload: a physical Model plus the Experiment describing
// how to run it. APIKey is transport-level authentication (checked when the
// server has API_KEY configured, ignored otherwise); it is redacted from the
// config.json persisted into the result bundle.
type Job struct {
	APIKey     string     `json:"api_key,omitempty"`
	Model      Model      `json:"model"`
	Experiment Experiment `json:"experiment"`
}
