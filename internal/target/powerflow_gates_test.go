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

// --- ADR-0010: the network inside the optimization -------------------------

func lopfJob(t *testing.T, mut func(j *model.Job)) model.Job {
	t.Helper()
	j, err := scenarios.Load("pypsa_lopf.json")
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(&j)
	}
	return j
}

// The linear-power-flow network model is a capability PyPSA alone claims; a
// transport solve of a payload that asked for the voltage law would be a
// different problem, so the other targets reject it like a mode.
func TestLinearPowerFlowGatedToPyPSA(t *testing.T) {
	for _, tgt := range []model.Target{model.TargetCalliope, model.TargetCalliope06, model.TargetAdOpt} {
		j := lopfJob(t, nil)
		wantGateErr(t, &j, tgt, `network model "linear_power_flow" is not supported by target "`+string(tgt)+`"`)
	}
	j := lopfJob(t, nil)
	warns, err := target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatalf("pypsa should accept the lopf scenario: %v", err)
	}
	if hasWarning(warns, "power-flow inputs") {
		t.Errorf("the electrical arcs and nodes are used by the voltage law, not ignored: %v", warns)
	}
	if !target.Supports(model.TargetPyPSA, model.FeatLinearPowerFlow) {
		t.Error("pypsa must claim linear_power_flow")
	}
	// The same payload as a transport model is still fine everywhere PyPSA
	// and Calliope accept transmission, with the electrical fields reported.
	j = lopfJob(t, func(j *model.Job) { j.Experiment.Network = nil })
	for _, tgt := range []model.Target{model.TargetPyPSA, model.TargetCalliope} {
		warns, err := target.ValidateFor(&j, tgt)
		if err != nil {
			t.Fatalf("%s: transport model of the lopf payload: %v", tgt, err)
		}
		if !hasWarning(warns, "power-flow inputs", "transmission [ab ac bc]") {
			t.Errorf("%s: want the ignored-inputs warning, got %v", tgt, warns)
		}
	}
}

// Inside a linear-power-flow optimization the network needs what a power
// flow needs — a voltage level on every bus, impedances on the arcs it
// solves — and dispatch-like inputs are still ignored with the warning.
func TestLinearPowerFlowPyPSAGates(t *testing.T) {
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
		}, `network model "linear_power_flow" needs at least one transmission with electrical parameters`},
		{"bus without voltage level", func(j *model.Job) { j.Model.Nodes["b"] = model.Node{} },
			`node "b" hosts electricity components but has no electrical.v_nom`},
		{"outage of an arc without impedance", func(j *model.Job) {
			j.Experiment.Network.Security = &model.SecurityOptions{Outages: []string{"tie"}}
			j.Model.Transmission["tie"] = model.Transmission{Carrier: "electricity", From: "a", To: "c", Bidirectional: true, Capacity: &model.Capacity{Existing: 10}}
		}, `experiment.network.security.outages: "tie" is not an electrical arc`},
		{"unknown outage", func(j *model.Job) {
			j.Experiment.Network.Security = &model.SecurityOptions{Outages: []string{"zz"}}
		}, `experiment.network.security.outages: "zz" is not a transmission arc`},
		{"security with sidecar constraints", func(j *model.Job) {
			j.Experiment.Network.Security = &model.SecurityOptions{Outages: []string{"ab"}}
			j.Model.Constraints = []model.Constraint{{Name: "cap", Sense: "<=", Bound: 500, Terms: []model.Term{{Coefficient: 1, Variable: model.VarCapacity, Techs: []string{"cheap"}}}}}
		}, `security-constrained solve cannot apply portable constraints`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := lopfJob(t, c.mut)
			wantGateErr(t, &j, model.TargetPyPSA, c.want)
		})
	}

	// A controlled tie without impedance is allowed in the voltage-law
	// network: it stays a Link. A dispatch on a technology is ignored.
	j := lopfJob(t, func(j *model.Job) {
		j.Model.Transmission["tie"] = model.Transmission{Carrier: "electricity", From: "a", To: "c", Bidirectional: true, Capacity: &model.Capacity{Existing: 10}}
		g := j.Model.Technologies["cheap"]
		g.Dispatch = model.Num(5)
		j.Model.Technologies["cheap"] = g
	})
	warns, err := target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatalf("tie + dispatch: %v", err)
	}
	if !hasWarning(warns, "power-flow inputs", "technologies [cheap]") || hasWarning(warns, "transmission [") {
		t.Errorf("the warning names the ignored dispatch and nothing about the arcs, got %v", warns)
	}

	// Transmission investment without a way to annualize is reported, not
	// silently priced at zero.
	j = lopfJob(t, func(j *model.Job) {
		l := j.Model.Transmission["ab"]
		l.Lifetime, l.InterestRate = nil, nil
		j.Model.Transmission["ab"] = l
	})
	warns, err = target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(warns, `transmission "ab"`, "lifetime and interest_rate", "not emitted") {
		t.Errorf("want the annualization warning, got %v", warns)
	}
}

// --- ADR-0011: storage as a Store -------------------------------------------

func storeJob(t *testing.T, mut func(j *model.Job)) model.Job {
	t.Helper()
	j, err := scenarios.Load("pypsa_store.json")
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(&j)
	}
	return j
}

// With storage.energy_capacity a storage becomes a Store with charger and
// discharger on PyPSA, which lifts the earlier rejections of independent
// rates, depth of discharge and the energy-capacity cost; without it those
// fields still have no StorageUnit slot, and a Store has no inflow or fixed
// state-of-charge profile.
func TestStoreRepresentationGates(t *testing.T) {
	j := storeJob(t, nil)
	warns, err := target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatalf("store scenario should validate on pypsa: %v", err)
	}
	if hasWarning(warns, "investment_per_energy_capacity") {
		t.Errorf("the energy-capacity cost is priced on the Store, not dropped: %v", warns)
	}
	cases := []struct {
		name string
		mut  func(j *model.Job)
		want string
	}{
		{"rates without energy capacity", func(j *model.Job) {
			b := j.Model.Technologies["battery"]
			b.Storage.EnergyCapacity = nil
			j.Model.Technologies["battery"] = b
		}, `tech "battery": storage.max_charge_rate/max_discharge_rate need storage.energy_capacity on pypsa (the Store representation sizes energy and power independently)`},
		{"depth of discharge without energy capacity", func(j *model.Job) {
			b := j.Model.Technologies["battery"]
			b.Storage.EnergyCapacity, b.Storage.MaxChargeRate, b.Storage.MaxDischargeRate = nil, nil, nil
			j.Model.Technologies["battery"] = b
		}, `tech "battery": storage.depth_of_discharge needs storage.energy_capacity on pypsa`},
		{"inflow on a store", func(j *model.Job) {
			b := j.Model.Technologies["battery"]
			b.Storage.Inflow = model.Num(1)
			j.Model.Technologies["battery"] = b
		}, `tech "battery": storage.inflow has no slot on a Store; drop storage.energy_capacity to keep the StorageUnit representation`},
		{"soc profile on a store", func(j *model.Job) {
			b := j.Model.Technologies["battery"]
			b.Storage.SOCProfile = model.Num(10)
			j.Model.Technologies["battery"] = b
		}, `tech "battery": storage.soc_profile has no slot on a Store`},
		{"binary flow exclusion still rejected", func(j *model.Job) {
			b := j.Model.Technologies["battery"]
			b.Storage.NoSimultaneousChargeDischarge = true
			j.Model.Technologies["battery"] = b
		}, `no_simultaneous_charge_discharge is not representable for pypsa`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := storeJob(t, c.mut)
			wantGateErr(t, &j, model.TargetPyPSA, c.want)
		})
	}

	// Calliope and AdOpT keep their storage mappings; the fixed state-of-charge
	// profile they have no slot for is ignored with a warning.
	for _, tgt := range []model.Target{model.TargetCalliope, model.TargetAdOpt} {
		j := storeJob(t, func(j *model.Job) {
			if tgt == model.TargetAdOpt {
				// AdOpT maps only database technologies and has no minimum
				// state of charge: keep the storages, drop what it rejects.
				delete(j.Model.Technologies, "pv")
				delete(j.Model.Technologies, "grid")
				b := j.Model.Technologies["battery"]
				b.Storage.DepthOfDischarge = nil
				j.Model.Technologies["battery"] = b
			}
		})
		warns, err := target.ValidateFor(&j, tgt)
		if err != nil {
			t.Fatalf("%s: the store scenario must stay valid: %v", tgt, err)
		}
		if !hasWarning(warns, "soc_profile", "ignored", `target "`+string(tgt)+`"`) {
			t.Errorf("%s: want the soc_profile warning, got %v", tgt, warns)
		}
	}
}

// --- ADR-0011: modules, retirement, slack demand, copperplate ---------------

func scenarioJob(t *testing.T, file string, mut func(j *model.Job)) model.Job {
	t.Helper()
	j, err := scenarios.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(&j)
	}
	return j
}

// Continuous retirement, slack demand and copperplate are honored on PyPSA
// now; what still has no slot is named: a retirement cost, complete-only
// retirement, retirement of a Store, slack demand without a price, a
// copperplate under the voltage law.
func TestRetirementSlackCopperplateGates(t *testing.T) {
	for _, file := range []string{"pypsa_modular.json", "pypsa_retire_slack.json", "pypsa_copperplate.json"} {
		j := scenarioJob(t, file, nil)
		if _, err := target.ValidateFor(&j, model.TargetPyPSA); err != nil {
			t.Errorf("%s should validate on pypsa: %v", file, err)
		}
	}
	j := scenarioJob(t, "pypsa_copperplate.json", nil)
	warns, _ := target.ValidateFor(&j, model.TargetPyPSA)
	if !hasWarning(warns, "copperplate", "transmission [l12]", "dropped") {
		t.Errorf("copperplate should name the arcs it drops, got %v", warns)
	}
	j = scenarioJob(t, "pypsa_retire_slack.json", func(j *model.Job) {
		o := j.Model.Technologies["old"]
		o.Capacity.Decommission.Cost = fp(50)
		j.Model.Technologies["old"] = o
	})
	warns, err := target.ValidateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(warns, `tech "old"`, "decommission.cost", "not priced") {
		t.Errorf("a retirement cost has no PyPSA slot and must be warned about, got %v", warns)
	}

	cases := []struct {
		file string
		mut  func(j *model.Job)
		want string
	}{
		{"pypsa_retire_slack.json", func(j *model.Job) {
			o := j.Model.Technologies["old"]
			o.Capacity.Decommission.Mode = "only_complete"
			j.Model.Technologies["old"] = o
		}, `tech "old": capacity.decommission mode "only_complete" is not supported for pypsa`},
		{"pypsa_retire_slack.json", func(j *model.Job) { j.Experiment.AllowUnmetDemand.PenaltyPrice = nil },
			`allow_unmet_demand needs penalty_price on pypsa (the slack generators' marginal cost)`},
		{"pypsa_store.json", func(j *model.Job) {
			b := j.Model.Technologies["battery"]
			b.Capacity = &model.Capacity{Existing: 5, Decommission: &model.Decommission{Mode: "continuous"}}
			j.Model.Technologies["battery"] = b
		}, `tech "battery": continuous retirement of a Store-based storage is not supported for pypsa`},
		{"pypsa_copperplate.json", func(j *model.Job) {
			for _, n := range []string{"n1", "n2", "n3"} {
				j.Model.Nodes[n] = model.Node{Electrical: &model.NodeElectrical{VNom: 110}}
			}
			l := j.Model.Transmission["l12"]
			l.Electrical = &model.TransmissionElectrical{Kind: model.ElectricalLine, R: 1, X: 10}
			j.Model.Transmission["l12"] = l
			j.Experiment.Network = &model.NetworkOptions{Model: model.NetworkLinearPowerFlow}
		}, `copperplate cannot be combined with network model "linear_power_flow"`},
	}
	for _, c := range cases {
		t.Run(c.want[:24], func(t *testing.T) {
			j := scenarioJob(t, c.file, c.mut)
			wantGateErr(t, &j, model.TargetPyPSA, c.want)
		})
	}
	// Calliope keeps its own mappings for these fields: the slack payload
	// validates there as before (ensure_feasibility), the copperplate one is
	// still rejected there, the modular one accepted.
	j = scenarioJob(t, "pypsa_retire_slack.json", nil)
	if _, err := target.ValidateFor(&j, model.TargetCalliope); err != nil {
		t.Errorf("calliope: slack payload should still validate: %v", err)
	}
	j = scenarioJob(t, "pypsa_copperplate.json", nil)
	if _, err := target.ValidateFor(&j, model.TargetCalliope); err == nil {
		t.Error("calliope: copperplate stays rejected there")
	}
	j = scenarioJob(t, "pypsa_modular.json", nil)
	if _, err := target.ValidateFor(&j, model.TargetCalliope); err != nil {
		t.Errorf("calliope: modular payload should validate: %v", err)
	}
}
