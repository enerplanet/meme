// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

// acCarrier is the bus carrier PyPSA treats as an electrical sub-network: the
// power-flow routines build an admittance matrix for "AC" buses only, so the
// electrical carrier's buses are emitted under this name in mode power_flow.
const acCarrier = "AC"

// dispatchFile maps a role onto the time-varying p_set file its component
// reads in a power flow.
var dispatchFile = map[model.Role]string{
	model.RoleSupply:     "generators-p_set.csv",
	model.RoleStorage:    "storage_units-p_set.csv",
	model.RoleConversion: "links-p_set.csv",
}

// powerFlow is the resolved power-flow configuration of a job (ADR-0008): the
// carrier whose network is solved, the emitted name of the slack generator,
// the voltage set-points per node (pv generators and the slack), and the run
// options. powerFlowOf yields it for mode power_flow only.
type powerFlow struct {
	carrier   string
	slack     string
	setpoints map[string]float64
	opts      *model.PowerFlowOptions
}

// powerFlowOf resolves the power-flow configuration of a job that validated
// for mode power_flow; ok is false for every other mode.
func powerFlowOf(j *model.Job) (powerFlow, bool) {
	if j.Experiment.EffectiveMode() != model.ModePowerFlow {
		return powerFlow{}, false
	}
	carriers := electricalCarriers(&j.Model)
	pf := powerFlow{opts: j.Experiment.PowerFlow}
	if pf.opts == nil {
		pf.opts = &model.PowerFlowOptions{}
	}
	if len(carriers) > 0 {
		pf.carrier = carriers[0]
	}
	if name, _, err := slackComponent(&j.Model, pf.carrier, pf.opts.Slack); err == nil {
		pf.slack = name
	}
	pf.setpoints = voltageSetpoints(&j.Model, pf.carrier, pf.slack)
	return pf, true
}

// voltageSetpoints collects the bus voltage set-point per node: from the pv
// generators placed there and from the slack. ValidateJob guarantees they
// agree, so the first one found stands.
func voltageSetpoints(m *model.Model, carrier, slack string) map[string]float64 {
	out := map[string]float64{}
	for _, id := range emit.Keys(m.Technologies) {
		t := m.Technologies[id]
		if t.Role != model.RoleSupply || t.CarrierOut.First() != carrier {
			continue
		}
		for _, node := range t.Node {
			eff := t.At(node)
			e := eff.Electrical
			if e == nil || e.VoltageSetpoint == nil || !(e.IsPV() || componentName(id, t, node) == slack) {
				continue
			}
			if _, seen := out[node]; !seen {
				out[node] = *e.VoltageSetpoint
			}
		}
	}
	return out
}

// acIslands partitions the AC nodes into the components the electrical arcs
// connect (a controlled link joins nothing), each sorted, the list sorted by
// its first node. Every island is a PyPSA sub-network with its own slack.
func acIslands(m *model.Model, carrier string, acNodes []string) [][]string {
	parent := map[string]string{}
	for _, n := range acNodes {
		parent[n] = n
	}
	var find func(string) string
	find = func(n string) string {
		for parent[n] != n {
			parent[n] = parent[parent[n]]
			n = parent[n]
		}
		return n
	}
	for _, l := range m.Transmission {
		if l.Carrier != carrier || l.Electrical == nil || !l.IsActive() {
			continue
		}
		if _, ok := parent[l.From]; !ok {
			continue
		}
		if _, ok := parent[l.To]; !ok {
			continue
		}
		parent[find(l.From)] = find(l.To)
	}
	groups := map[string][]string{}
	for _, n := range acNodes {
		r := find(n)
		groups[r] = append(groups[r], n)
	}
	var out [][]string
	for _, g := range groups {
		sort.Strings(g)
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// electricalCarriers lists the distinct carriers of the arcs that carry
// electrical parameters, sorted.
func electricalCarriers(m *model.Model) []string {
	seen := map[string]bool{}
	for _, l := range m.Transmission {
		if l.Electrical != nil {
			seen[l.Carrier] = true
		}
	}
	return emit.Keys(seen)
}

// slackComponent resolves experiment.power_flow.slack ("tech" or "tech@node")
// onto the emitted generator name of a supply technology on the electrical
// carrier. A technology placed at several nodes must be spelled tech@node.
func slackComponent(m *model.Model, carrier, slack string) (name, node string, err error) {
	if slack == "" {
		return "", "", fmt.Errorf("experiment.power_flow.slack is required: the supply technology that balances the AC network (id, or id@node)")
	}
	tid, nid, hasNode := strings.Cut(slack, "@")
	t, ok := m.Technologies[tid]
	if !ok || t.Role != model.RoleSupply || t.CarrierOut.First() != carrier {
		return "", "", fmt.Errorf("experiment.power_flow.slack %q names no supply technology on the %s network", slack, carrier)
	}
	switch {
	case hasNode && !model.ContainsStr(t.Node, nid):
		return "", "", fmt.Errorf("experiment.power_flow.slack %q: tech %q is not placed at node %q", slack, tid, nid)
	case !hasNode && len(t.Node) > 1:
		return "", "", fmt.Errorf("experiment.power_flow.slack %q: tech %q is placed at %v; spell the slack as id@node", slack, tid, t.Node)
	case !hasNode:
		nid = t.Node[0]
	}
	return componentName(tid, t, nid), nid, nil
}

// componentName is the emitted name of a technology's instance at a node: the
// id alone for a single-node technology, id@node otherwise (as Emit names it).
func componentName(id string, t model.Technology, node string) string {
	if len(t.Node) > 1 {
		return id + "@" + node
	}
	return id
}

// validatePowerFlow holds the PyPSA preconditions of mode power_flow: one AC
// network with a voltage level on every bus it touches and an impedance and a
// rating on every arc of it, a named slack, and a fixed dispatch for every
// injection that is not the slack. The generic gates already rejected every
// decision variable (expandable capacities, trade, constraints, scenarios).
func validatePowerFlow(j *model.Job) ([]string, error) {
	m := &j.Model
	carriers := electricalCarriers(m)
	switch len(carriers) {
	case 0:
		return nil, fmt.Errorf("mode \"power_flow\" needs at least one transmission with electrical parameters (kind, r, x): the arcs of the AC network to solve")
	case 1:
	default:
		return nil, fmt.Errorf("electrical arcs span carriers %v; a power flow solves one AC network", carriers)
	}
	carrier := carriers[0]

	for _, id := range emit.Keys(m.Transmission) {
		l := m.Transmission[id]
		if l.Carrier == carrier && l.Electrical == nil && l.Dispatch == nil {
			return nil, fmt.Errorf("transmission %q carries %s without electrical parameters or a dispatch; a power flow needs kind, r and x on a branch or a fixed transfer on a link", id, carrier)
		}
		if l.Electrical != nil {
			if _, ok := l.Electrical.Rating(l.Capacity); !ok {
				return nil, fmt.Errorf("transmission %q needs electrical.s_nom or capacity.existing (the branch rating for the loading output)", id)
			}
		}
	}

	// Every bus of the network needs a voltage level; the other carriers'
	// buses ride along as fixed injections the power flow does not balance.
	grid := busGrid(m)
	var acNodes, others []string
	otherSet := map[string]bool{}
	for pair := range grid {
		if pair[1] == carrier {
			acNodes = append(acNodes, pair[0])
		} else {
			otherSet[pair[1]] = true
		}
	}
	sort.Strings(acNodes)
	for _, node := range acNodes {
		if n := m.Nodes[node]; n.Electrical == nil || n.Electrical.VNom <= 0 {
			return nil, fmt.Errorf("node %q hosts %s components but has no electrical.v_nom (the voltage level of its AC bus)", node, carrier)
		}
	}
	others = emit.Keys(otherSet)

	slackName, slackNode, err := slackComponent(m, carrier, j.Experiment.PowerFlow.SlackOrEmpty())
	if err != nil {
		return nil, err
	}
	// PyPSA distributes the balance in proportion to each generator's p_set;
	// a slack without one would get nothing and the other generators would
	// absorb the whole imbalance.
	if j.Experiment.PowerFlow.DistributeSlack {
		tid, _, _ := strings.Cut(j.Experiment.PowerFlow.Slack, "@")
		if m.Technologies[tid].At(slackNode).Dispatch == nil {
			return nil, fmt.Errorf("experiment.power_flow.distribute_slack shares the balance in proportion to each generator's dispatch, so the slack %q needs a dispatch too (its expected output); otherwise PyPSA moves the whole balance onto the other generators", slackName)
		}
	}
	// Voltage set-points: a pv generator holds its bus, the slack may name
	// its own; anyone else's set-point would be silently ignored. All
	// set-points at one node must agree (PyPSA has one per bus).
	type setpoint struct {
		name string
		v    float64
	}
	byNode := map[string][]setpoint{}
	for _, id := range emit.Keys(m.Technologies) {
		t := m.Technologies[id]
		if t.Role == model.RoleDemand || !t.IsActive() {
			continue
		}
		for _, node := range t.Node {
			name := componentName(id, t, node)
			eff := t.At(node)
			if t.Role == model.RoleSupply && t.CarrierOut.First() == carrier {
				if e := eff.Electrical; e != nil && e.VoltageSetpoint != nil && !e.IsPV() && name != slackName {
					return nil, fmt.Errorf("tech %q: electrical.voltage_setpoint needs control \"pv\" or the slack role (a PQ generator does not hold its bus voltage)", name)
				}
				if e := eff.Electrical; name == slackName || e.IsPV() {
					v := 1.0
					if e != nil && e.VoltageSetpoint != nil {
						v = *e.VoltageSetpoint
					}
					byNode[node] = append(byNode[node], setpoint{name, v})
				}
			}
			if name == slackName {
				continue
			}
			if eff.Dispatch == nil {
				return nil, fmt.Errorf("mode \"power_flow\" needs a fixed dispatch for tech %q (only the slack %q balances); set technology.dispatch", name, slackName)
			}
		}
	}
	for _, node := range emit.Keys(byNode) {
		sps := byNode[node]
		for _, sp := range sps[1:] {
			if sp.v != sps[0].v {
				return nil, fmt.Errorf("node %q: voltage set-points disagree (%s %g, %s %g); one bus holds one voltage", node, sps[0].name, sps[0].v, sp.name, sp.v)
			}
		}
	}

	// Every AC island (the nodes the branches connect; a controlled link
	// joins nothing) is a PyPSA sub-network that needs a generator to balance
	// it. The named slack balances its own island; PyPSA balances any other
	// on its largest generator, which is worth knowing.
	var warns []string
	for _, island := range acIslands(m, carrier, acNodes) {
		inIsland := map[string]bool{}
		for _, n := range island {
			inIsland[n] = true
		}
		var largest string
		largestCap := -1.0
		hasSlack := false
		for _, id := range emit.Keys(m.Technologies) {
			t := m.Technologies[id]
			if t.Role != model.RoleSupply || t.CarrierOut.First() != carrier || !t.IsActive() {
				continue
			}
			for _, node := range t.Node {
				if !inIsland[node] {
					continue
				}
				name := componentName(id, t, node)
				if name == slackName {
					hasSlack = true
				}
				c := 0.0
				if cap := t.At(node).Capacity; cap != nil {
					c = cap.Existing
				}
				if c > largestCap {
					largest, largestCap = name, c
				}
			}
		}
		if largest == "" {
			return nil, fmt.Errorf("AC island %v has no supply technology to balance it; PyPSA cannot solve a sub-network without a generator", island)
		}
		if !hasSlack {
			warns = append(warns, fmt.Sprintf("AC island %v is balanced by PyPSA on its largest generator (%q), not by the slack %q", island, largest, slackName))
		}
	}

	if j.Experiment.PowerFlow.EffectiveMethod() == model.PowerFlowLinear && hasReactiveInputs(m) {
		warns = append(warns, "method \"linear\" ignores reactive power, voltage control and shunts: the DC solve carries active power only")
	}
	if len(others) > 0 {
		warns = append(warns, fmt.Sprintf("power flow balances the %s network only; carriers %v are carried as fixed injections and not balanced", carrier, others))
	}
	if s := j.Experiment.Solver; s.TimeLimit != nil || s.MIPGap != nil || s.Threads != nil || len(s.Options) > 0 {
		warns = append(warns, "solver settings are not used by a power flow (it is not an optimization)")
	}
	return warns, nil
}

// hasReactiveInputs reports whether any technology or node carries a
// reactive-power, voltage-control or shunt field.
func hasReactiveInputs(m *model.Model) bool {
	for _, t := range m.Technologies {
		if t.Electrical != nil {
			return true
		}
		for _, ov := range t.NodeOverrides {
			if ov.Electrical != nil {
				return true
			}
		}
	}
	for _, n := range m.Nodes {
		if n.Electrical != nil && n.Electrical.Shunt != nil {
			return true
		}
	}
	return false
}

// qFile maps a role onto the time-varying q_set file its component reads.
var qFile = map[model.Role]string{
	model.RoleSupply:  "generators-q_set.csv",
	model.RoleDemand:  "loads-q_set.csv",
	model.RoleStorage: "storage_units-q_set.csv",
}

// activeSetpoint is the Value a component's active power is fixed by: the
// dispatch for an injection, the demand profile for a load.
func activeSetpoint(t model.Technology) *model.Value {
	if t.Role == model.RoleDemand {
		return t.DemandProfile
	}
	return t.Dispatch
}

// injection writes a component's power-flow attributes: the fixed p_set for
// an injection (a scalar here, a series through MaterializeTimeSeries), the
// reactive q_set for supply, demand and storage (a scalar set-point, a scalar
// derived from a scalar active set-point at the power factor, else 0 with
// the series in its file) and, for generators, the control type — Slack for
// the balancing technology, PV for a voltage-holding one, PQ otherwise.
func (pf powerFlow) injection(row map[string]string, name string, t model.Technology) {
	if _, ok := dispatchFile[t.Role]; ok {
		if f, ok := t.Dispatch.Reduce(); ok {
			row["p_set"] = emit.Ftoa(f)
		} else {
			row["p_set"] = "0"
		}
	}
	if _, ok := qFile[t.Role]; ok {
		row["q_set"] = "0"
		if e := t.Electrical; e != nil {
			if q, ok := e.ReactiveDispatch.Reduce(); ok {
				row["q_set"] = emit.Ftoa(q)
			} else if e.PowerFactor != nil {
				if p, ok := activeSetpoint(t).Reduce(); ok {
					row["q_set"] = emit.Ftoa(math.Round(p*e.ReactiveFactor()*1e12) / 1e12)
				}
			}
		}
	}
	if t.Role == model.RoleSupply {
		row["control"] = "PQ"
		switch {
		case name == pf.slack:
			row["control"] = "Slack"
		case t.Electrical.IsPV():
			row["control"] = "PV"
		}
	}
}

// shuntRows builds the ShuntImpedance table: one row per node with a shunt on
// the electrical carrier's bus.
func (pf powerFlow) shuntRows(m *model.Model) [][2]map[string]string {
	var rows [][2]map[string]string
	for _, id := range emit.Keys(m.Nodes) {
		n := m.Nodes[id]
		if n.Electrical == nil || n.Electrical.Shunt == nil {
			continue
		}
		rows = append(rows, [2]map[string]string{{
			"name": id + "::shunt", "bus": BusID(id, pf.carrier),
			"g": emit.Ftoa(n.Electrical.Shunt.G), "b": emit.Ftoa(n.Electrical.Shunt.B),
		}})
	}
	return rows
}

// transfer writes the fixed p_set of a controlled link (an arc with a
// dispatch and no impedance): a scalar here, a series through
// MaterializeTimeSeries.
func (pf powerFlow) transfer(row map[string]string, l model.Transmission) {
	if l.Dispatch == nil {
		return
	}
	if f, ok := l.Dispatch.Reduce(); ok {
		row["p_set"] = emit.Ftoa(f)
	} else {
		row["p_set"] = "0"
	}
}

// branch builds the Line or Transformer row of an electrical arc.
func (pf powerFlow) branch(id string, l model.Transmission) map[string]string {
	e := l.Electrical
	rating, _ := e.Rating(l.Capacity)
	r, x := e.Impedance(l.Distance)
	row := map[string]string{
		"name": id, "bus0": BusID(l.From, l.Carrier), "bus1": BusID(l.To, l.Carrier),
		"r": emit.Ftoa(r), "x": emit.Ftoa(x), "s_nom": emit.Ftoa(rating),
		"num_parallel": fmt.Sprint(e.Circuits()),
	}
	if e.Kind == model.ElectricalTransformer {
		row["tap_ratio"] = emit.Ftoa(e.Tap())
		if e.PhaseShift != nil {
			row["phase_shift"] = emit.Ftoa(*e.PhaseShift)
		}
	} else {
		row["length"] = "0"
		if l.Distance != nil {
			row["length"] = emit.Ftoa(*l.Distance)
		}
		if e.B != nil {
			row["b"] = emit.Ftoa(*e.B)
		}
		if e.G != nil {
			row["g"] = emit.Ftoa(*e.G)
		}
	}
	if !l.IsActive() {
		row["active"] = "False"
	}
	return row
}
