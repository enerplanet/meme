// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package target_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
)

// powerFlowJob loads the power-flow scenario; mut adapts it for one case.
func powerFlowJob(t *testing.T, mut func(j *model.Job)) model.Job {
	t.Helper()
	j, err := scenarios.Load("pypsa_power_flow.json")
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(&j)
	}
	return j
}

func wantGateErr(t *testing.T, j *model.Job, tgt model.Target, want string) {
	t.Helper()
	_, err := target.ValidateFor(j, tgt)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: want error containing %q, got: %v", tgt, want, err)
	}
}

func hasWarning(ws []string, parts ...string) bool {
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

// The mode routes through the power_flow capability, which only PyPSA claims.
func TestPowerFlowModeGatedToPyPSA(t *testing.T) {
	for _, tgt := range []model.Target{model.TargetCalliope, model.TargetCalliope06, model.TargetAdOpt} {
		j := powerFlowJob(t, nil)
		wantGateErr(t, &j, tgt, `mode "power_flow" is not supported by target "`+string(tgt)+`"`)
	}
	j := powerFlowJob(t, nil)
	if _, err := target.ValidateFor(&j, model.TargetPyPSA); err != nil {
		t.Fatalf("pypsa should accept the power-flow scenario: %v", err)
	}
	if !target.Supports(model.TargetPyPSA, model.FeatPowerFlow) {
		t.Error("pypsa must claim power_flow")
	}
}

// Power-flow inputs — dispatch and the electrical blocks — are ignored with
// one warning wherever no power flow runs (ADR-0009): on Calliope and AdOpT,
// which have no such concept, and on PyPSA's optimization modes. The payload
// stays valid, so one document serves the planning call and the power-flow
// call; the warning names what was ignored and where to pin dispatch instead.
func TestPowerFlowInputsIgnoredOutsidePowerFlow(t *testing.T) {
	toPlan := func(j *model.Job) {
		j.Experiment.Mode = model.ModePlan
		j.Experiment.PowerFlow = nil
	}
	for _, tgt := range []model.Target{model.TargetPyPSA, model.TargetCalliope} {
		j, err := scenarios.Load("pypsa_power_flow_reactive.json")
		if err != nil {
			t.Fatal(err)
		}
		toPlan(&j)
		warns, err := target.ValidateFor(&j, tgt)
		if err != nil {
			t.Fatalf("%s: power-flow inputs in mode plan must not reject: %v", tgt, err)
		}
		if !hasWarning(warns, "power-flow inputs", "ignored", `mode "plan"`, "technologies [dg grid load1 load2 load3 pv]", "transmission [hvdc l12 trafo]", "nodes [hv mv1 mv2 mv3]", "operation.equals_pu") {
			t.Errorf("%s: want one warning naming the ignored inputs, got %v", tgt, warns)
		}
	}
	// AdOpT has no transmission and maps only database technologies, so its
	// case starts from one of its own payloads with the inputs added.
	ja, err := scenarios.Load("adopt_storage.json")
	if err != nil {
		t.Fatal(err)
	}
	batt := ja.Model.Technologies["batt"]
	batt.Dispatch = model.Num(1)
	batt.Electrical = &model.TechElectrical{PowerFactor: fp(0.95)}
	ja.Model.Technologies["batt"] = batt
	n1 := ja.Model.Nodes["n1"]
	n1.Electrical = &model.NodeElectrical{VNom: 20}
	ja.Model.Nodes["n1"] = n1
	warns, err := target.ValidateFor(&ja, model.TargetAdOpt)
	if err != nil {
		t.Fatalf("adopt-net0: power-flow inputs must not reject: %v", err)
	}
	if !hasWarning(warns, "power-flow inputs", "ignored", `target "adopt-net0"`, "technologies [batt]", "nodes [n1]") {
		t.Errorf("adopt-net0: want the ignored-inputs warning, got %v", warns)
	}
	// An override-only input is reported as well.
	j := powerFlowJob(t, func(j *model.Job) {
		toPlan(j)
		pv := j.Model.Technologies["pv"]
		pv.Dispatch = nil
		pv.NodeOverrides = map[string]model.NodeOverride{"mv2": {Dispatch: model.Num(1)}}
		j.Model.Technologies["pv"] = pv
	})
	warns, err = target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(warns, "power-flow inputs", "technologies [pv]") {
		t.Errorf("override-only dispatch should be named, got %v", warns)
	}
}

// Electrical parameters outside a power flow are not an error — the same
// model serves the planning call and the power-flow call — but they are
// named as unused, and the arcs are emitted as transport links.
func TestElectricalParametersWarnOutsidePowerFlow(t *testing.T) {
	j := powerFlowJob(t, func(j *model.Job) {
		j.Experiment.Mode = model.ModePlan
		j.Experiment.PowerFlow = nil
		pv := j.Model.Technologies["pv"]
		pv.Dispatch = nil
		j.Model.Technologies["pv"] = pv
	})
	for _, tgt := range []model.Target{model.TargetPyPSA, model.TargetCalliope} {
		jj := j
		warns, err := target.ValidateFor(&jj, tgt)
		if err != nil {
			t.Fatalf("%s: electrical parameters in mode plan must not reject: %v", tgt, err)
		}
		if !hasWarning(warns, "power-flow inputs", "ignored", "l12", "trafo", "hv", "transport links") {
			t.Errorf("%s: want one warning naming the unused electrical parameters, got %v", tgt, warns)
		}
	}
}

// A power flow has no decision variables: anything the optimizer would have
// decided is rejected with a pointed message rather than silently fixed.
func TestPowerFlowRejectsDecisions(t *testing.T) {
	cases := []struct {
		name string
		mut  func(j *model.Job)
		want string
	}{
		{"expandable tech", func(j *model.Job) {
			pv := j.Model.Technologies["pv"]
			pv.Capacity = &model.Capacity{Existing: 10, Expandable: true, Max: fp(50)}
			j.Model.Technologies["pv"] = pv
		}, `mode "power_flow" has no decision variables, but tech "pv" is expandable`},
		{"expandable arc", func(j *model.Job) {
			l := j.Model.Transmission["l12"]
			l.Capacity = &model.Capacity{Existing: 20, Expandable: true, Max: fp(50)}
			j.Model.Transmission["l12"] = l
		}, `mode "power_flow" has no decision variables, but transmission "l12" is expandable`},
		{"trade", func(j *model.Job) {
			j.Model.Trade = map[string]model.Trade{"grid": {Node: "hv", Carrier: "electricity", Import: &model.TradeSide{Limit: model.Num(50), Price: model.Num(60)}}}
		}, `mode "power_flow" has no decision variables, but the payload carries trade`},
		{"constraints", func(j *model.Job) {
			j.Model.Constraints = []model.Constraint{{Name: "cap", Sense: "<=", Bound: 100, Terms: []model.Term{{Variable: model.VarCapacity, Techs: []string{"pv"}}}}}
		}, `mode "power_flow" has no decision variables, but the payload carries constraints`},
		{"emission limits", func(j *model.Job) {
			j.Model.EmissionLimits = []model.EmissionLimit{{Sense: "<=", Limit: 1000}}
		}, `mode "power_flow" has no decision variables, but the payload carries emission_limits`},
		{"scenarios", func(j *model.Job) {
			j.Experiment.Scenarios = []model.Scenario{{Name: "high", Weight: 1}}
		}, `mode "power_flow" has no decision variables, but the payload carries scenarios`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := powerFlowJob(t, c.mut)
			wantGateErr(t, &j, model.TargetPyPSA, c.want)
		})
	}
}

// The PyPSA-specific preconditions of a power flow: one AC network with a
// voltage level on every bus it touches, impedances on every arc of it, a
// named slack, and a fixed dispatch for everything that is not the slack.
func TestPowerFlowPyPSAGates(t *testing.T) {
	cases := []struct {
		name string
		mut  func(j *model.Job)
		want string
	}{
		{"no electrical arcs", func(j *model.Job) {
			for id, l := range j.Model.Transmission {
				l.Electrical = nil
				j.Model.Transmission[id] = l
			}
		}, `mode "power_flow" needs at least one transmission with electrical parameters`},
		{"electricity arc without impedance or transfer", func(j *model.Job) {
			l := j.Model.Transmission["l12"]
			l.Electrical = nil
			j.Model.Transmission["l12"] = l
		}, `transmission "l12" carries electricity without electrical parameters or a dispatch; a power flow needs kind, r and x on a branch or a fixed transfer on a link`},
		{"bus without voltage level", func(j *model.Job) {
			j.Model.Nodes["mv2"] = model.Node{Coords: j.Model.Nodes["mv2"].Coords}
		}, `node "mv2" hosts electricity components but has no electrical.v_nom`},
		{"slack missing", func(j *model.Job) { j.Experiment.PowerFlow = &model.PowerFlowOptions{} },
			`experiment.power_flow.slack is required`},
		{"slack unknown", func(j *model.Job) { j.Experiment.PowerFlow.Slack = "nope" },
			`experiment.power_flow.slack "nope" names no supply technology`},
		{"slack is a load", func(j *model.Job) { j.Experiment.PowerFlow.Slack = "load1" },
			`experiment.power_flow.slack "load1" names no supply technology`},
		{"supply without dispatch", func(j *model.Job) {
			pv := j.Model.Technologies["pv"]
			pv.Dispatch = nil
			j.Model.Technologies["pv"] = pv
		}, `mode "power_flow" needs a fixed dispatch for tech "pv" (only the slack "grid" balances)`},
		{"storage without dispatch", func(j *model.Job) {
			j.Model.Technologies["batt"] = model.Technology{Role: model.RoleStorage, Node: model.StringList{"mv1"},
				CarrierOut: model.StringList{"electricity"}, Capacity: &model.Capacity{Existing: 5},
				Storage: &model.Storage{MaxHours: fp(2)}}
		}, `mode "power_flow" needs a fixed dispatch for tech "batt"`},
		{"line without rating", func(j *model.Job) {
			l := j.Model.Transmission["l12"]
			l.Electrical.SNom = nil
			l.Capacity = &model.Capacity{}
			j.Model.Transmission["l12"] = l
		}, `transmission "l12" needs electrical.s_nom or capacity.existing (the branch rating for the loading output)`},
		{"distributed slack without a dispatch on the slack", func(j *model.Job) {
			j.Experiment.PowerFlow.DistributeSlack = true
		}, `experiment.power_flow.distribute_slack shares the balance in proportion to each generator's dispatch, so the slack "grid" needs a dispatch too`},
		{"two electrical carriers", func(j *model.Job) {
			j.Model.Carriers["heat"] = model.Carrier{}
			j.Model.Nodes["h1"] = model.Node{Electrical: &model.NodeElectrical{VNom: 20}}
			j.Model.Transmission["pipe"] = model.Transmission{Carrier: "heat", From: "mv1", To: "h1", Bidirectional: true,
				Capacity: &model.Capacity{Existing: 5}, Electrical: &model.TransmissionElectrical{Kind: model.ElectricalLine, R: 0.1, X: 0.2}}
		}, `electrical arcs span carriers [electricity heat]; a power flow solves one AC network`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := powerFlowJob(t, c.mut)
			wantGateErr(t, &j, model.TargetPyPSA, c.want)
		})
	}

	// The slack may be spelled tech@node, as the emitted component is named.
	j := powerFlowJob(t, func(j *model.Job) { j.Experiment.PowerFlow.Slack = "grid@hv" })
	if _, err := target.ValidateFor(&j, model.TargetPyPSA); err != nil {
		t.Errorf("slack spelled tech@node should be accepted: %v", err)
	}
	// With a dispatch on the slack, distributing the balance is fine.
	j = powerFlowJob(t, func(j *model.Job) {
		j.Experiment.PowerFlow.DistributeSlack = true
		g := j.Model.Technologies["grid"]
		g.Dispatch = model.Num(16)
		j.Model.Technologies["grid"] = g
	})
	if _, err := target.ValidateFor(&j, model.TargetPyPSA); err != nil {
		t.Errorf("distribute_slack with a slack dispatch should be accepted: %v", err)
	}

	// Warnings: other carriers ride along unbalanced; solver settings are moot.
	j = powerFlowJob(t, func(j *model.Job) {
		j.Model.Carriers["heat"] = model.Carrier{}
		j.Model.Technologies["boiler"] = model.Technology{Role: model.RoleConversion, Node: model.StringList{"mv1"},
			CarrierIn: model.StringList{"electricity"}, CarrierOut: model.StringList{"heat"},
			Capacity: &model.Capacity{Existing: 2}, Dispatch: model.Num(1)}
		j.Experiment.Solver = model.Solver{Name: "highs", TimeLimit: fp(60)}
	})
	warns, err := target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatalf("conversion with dispatch should be accepted: %v", err)
	}
	if !hasWarning(warns, "balances the electricity network only", "heat") {
		t.Errorf("want the unbalanced-carriers warning, got %v", warns)
	}
	if !hasWarning(warns, "solver settings are not used by a power flow") {
		t.Errorf("want the solver-settings warning, got %v", warns)
	}
}

// reactiveJob loads the reactive power-flow scenario, optionally adapted.
func reactiveJob(t *testing.T, mut func(j *model.Job)) model.Job {
	t.Helper()
	j, err := scenarios.Load("pypsa_power_flow_reactive.json")
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(&j)
	}
	return j
}

// The reactive network's own preconditions: every AC island needs a supply
// technology (PyPSA balances an island without the named slack on its
// largest generator and says so), a voltage set-point belongs to a pv
// generator or the slack, set-points at one bus agree, and the linear method
// is told what it ignores.
func TestPowerFlowReactiveGates(t *testing.T) {
	j := reactiveJob(t, nil)
	warns, err := target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatalf("reactive scenario should validate: %v", err)
	}
	if !hasWarning(warns, "AC island [mv3]", `largest generator ("dg")`, `slack "grid"`) {
		t.Errorf("want the island warning, got %v", warns)
	}

	cases := []struct {
		name string
		mut  func(j *model.Job)
		want string
	}{
		{"island without a generator", func(j *model.Job) { delete(j.Model.Technologies, "dg") },
			`AC island [mv3] has no supply technology to balance it`},
		{"voltage set-point without pv control or the slack role", func(j *model.Job) {
			dg := j.Model.Technologies["dg"]
			dg.Electrical = &model.TechElectrical{VoltageSetpoint: fp(1.0)}
			j.Model.Technologies["dg"] = dg
		}, `tech "dg": electrical.voltage_setpoint needs control "pv" or the slack role`},
		{"disagreeing set-points at one bus", func(j *model.Job) {
			j.Model.Technologies["pv2"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"mv2"}, CarrierOut: model.StringList{"electricity"},
				Capacity: &model.Capacity{Existing: 2}, Dispatch: model.Num(1), Electrical: &model.TechElectrical{Control: model.ControlPV, VoltageSetpoint: fp(1.03)}}
		}, `node "mv2": voltage set-points disagree (pv 1, pv2 1.03)`},
		{"distributed slack still needs a slack dispatch", func(j *model.Job) { j.Experiment.PowerFlow.DistributeSlack = true },
			`experiment.power_flow.distribute_slack`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := reactiveJob(t, c.mut)
			wantGateErr(t, &j, model.TargetPyPSA, c.want)
		})
	}

	// Two pv generators at one bus with the same set-point are fine.
	j = reactiveJob(t, func(j *model.Job) {
		j.Model.Technologies["pv2"] = model.Technology{Role: model.RoleSupply, Node: model.StringList{"mv2"}, CarrierOut: model.StringList{"electricity"},
			Capacity: &model.Capacity{Existing: 2}, Dispatch: model.Num(1), Electrical: &model.TechElectrical{Control: model.ControlPV, VoltageSetpoint: fp(1.0)}}
	})
	if _, err := target.ValidateFor(&j, model.TargetPyPSA); err != nil {
		t.Errorf("agreeing set-points should be accepted: %v", err)
	}

	// The linear method solves active power only and says so.
	j = reactiveJob(t, func(j *model.Job) { j.Experiment.PowerFlow.Method = model.PowerFlowLinear })
	warns, err = target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(warns, `method "linear"`, "reactive power", "voltage control", "shunts") {
		t.Errorf("want the linear-method warning, got %v", warns)
	}
}
