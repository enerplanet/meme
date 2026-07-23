// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// keysOf returns a map's keys in sorted order, for deterministic messages.
func keysOf[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Validate checks the physical model's enum values and referential integrity
// (every node, carrier, and time-series reference resolves). It is
// target-agnostic; ValidateFor layers target capability checks on top.
func (m *Model) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if m.Metadata.Name == "" {
		add("metadata.name is required")
	}
	// The time horizon must be explicit and parseable: emitters would
	// otherwise fall back to defaults and silently change the model horizon.
	for _, ts := range []struct{ field, v string }{{"start", m.Time.Start}, {"end", m.Time.End}} {
		if ts.v == "" {
			add("time.%s is required", ts.field)
		} else if !parseableDate(ts.v) {
			add("time.%s %q is not an ISO 8601 timestamp", ts.field, ts.v)
		}
	}
	if s, okS := parseDate(m.Time.Start); okS {
		if e, okE := parseDate(m.Time.End); okE && !e.After(s) {
			add("time.end %q must be after time.start %q", m.Time.End, m.Time.Start)
		}
	}
	seenSteps := map[string]int{}
	for i, step := range m.Time.Timesteps {
		if first, dup := seenSteps[step]; dup {
			add("time.timesteps[%d] duplicates timesteps[%d] (%q)", i, first, step)
		} else {
			seenSteps[step] = i
		}
	}
	if m.Time.Weights != nil {
		if f, ok := m.Time.Weights.Reduce(); ok && f <= 0 {
			add("time.weights must be positive")
		}
	}
	if m.DiscountRate != nil && *m.DiscountRate < 0 {
		add("discount_rate must not be negative")
	}
	for i, p := range m.Periods {
		if p.Name == "" {
			add("periods[%d] has no name", i)
		}
		if p.LengthYears != nil && *p.LengthYears <= 0 {
			add("periods[%d] (%s): length_years must be positive", i, p.Name)
		}
		if p.ObjectiveWeight != nil && *p.ObjectiveWeight < 0 {
			add("periods[%d] (%s): objective_weight must not be negative", i, p.Name)
		}
	}
	if len(m.Nodes) == 0 {
		add("model has no nodes")
	}
	if len(m.Carriers) == 0 {
		add("model has no carriers")
	}

	// Identifier hygiene: see validID for why these keys are charset-limited.
	checkIDs := func(section string, keys []string) {
		for _, k := range keys {
			if !validID(k) {
				add("%s: id %q is invalid (allowed: %s)", section, k, idCharset)
			}
		}
	}
	checkIDs("carriers", keysOf(m.Carriers))
	checkIDs("nodes", keysOf(m.Nodes))
	checkIDs("technologies", keysOf(m.Technologies))
	checkIDs("transmission", keysOf(m.Transmission))
	checkIDs("trade", keysOf(m.Trade))
	for _, k := range keysOf(m.Timeseries) {
		if !validSeriesID(k) {
			add("timeseries: id %q is invalid (allowed: %s)", k, idCharset)
		}
	}

	hasNode := func(id string) bool { _, ok := m.Nodes[id]; return ok }
	hasCarrier := func(id string) bool { _, ok := m.Carriers[id]; return ok }
	hasSeries := func(v *Value) bool {
		if !v.IsSeries() {
			return true
		}
		_, ok := m.Timeseries[v.SeriesID()]
		return ok
	}
	if m.Time.Weights != nil && !hasSeries(m.Time.Weights) {
		add("time.weights series %q not defined", m.Time.Weights.SeriesID())
	}

	for _, id := range keysOf(m.Technologies) {
		t := m.Technologies[id]
		if !t.Role.Valid() {
			add("tech %q: role %q is invalid", id, t.Role)
		}
		if !t.CostBasis.Valid() {
			add("tech %q: cost_basis %q is invalid", id, t.CostBasis)
		}
		if len(t.Node) == 0 {
			add("tech %q: no node specified", id)
		}
		for _, n := range t.Node {
			if !hasNode(n) {
				add("tech %q: node %q not defined", id, n)
			}
		}
		for _, c := range append(append(StringList{}, t.CarrierIn...), t.CarrierOut...) {
			if !hasCarrier(c) {
				add("tech %q: carrier %q not defined", id, c)
			}
		}
		if t.Role == RoleStorage && t.Storage == nil {
			add("tech %q: role is storage but no storage block given", id)
		}
		if t.Role == RoleDemand && t.DemandProfile == nil {
			add("tech %q: role is demand but no demand_profile given", id)
		}
		for _, v := range []*Value{t.Efficiency, t.DemandProfile} {
			if v != nil && !hasSeries(v) {
				add("tech %q: time-series %q not defined", id, v.SeriesID())
			}
		}
		if t.Operation != nil {
			for _, v := range []*Value{t.Operation.MaxPU, t.Operation.MinPU} {
				if v != nil && !hasSeries(v) {
					add("tech %q: time-series %q not defined", id, v.SeriesID())
				}
			}
		}
		if t.CostBasis == CostOvernight || t.CostBasis == "" {
			for cc, c := range t.Costs {
				if c.InvestmentPerCapacity != nil && (t.Lifetime == nil || t.InterestRate == nil) {
					add("tech %q: overnight investment in cost class %q needs lifetime and interest_rate to annualize", id, cc)
				}
			}
		}

		// Multi-port flows: at least one input, at most one reference (which
		// must be an input), and an explicit positive ratio on every
		// non-reference port.
		if len(t.Flows) > 0 {
			ins := 0
			refs := 0
			for fi, f := range t.Flows {
				if !hasCarrier(f.Carrier) {
					add("tech %q: flow carrier %q not defined", id, f.Carrier)
				}
				if f.Direction != FlowIn && f.Direction != FlowOut {
					add("tech %q: flow direction %q must be \"in\" or \"out\"", id, f.Direction)
				}
				if f.Direction == FlowIn {
					ins++
				}
				if f.Reference {
					refs++
					if f.Direction != FlowIn {
						add("tech %q: flows[%d] (%s) marks an output as the reference; the reference must be an input flow", id, fi, f.Carrier)
					}
				} else if f.Ratio <= 0 {
					// The reference input is its own basis (implicit ratio 1);
					// every other flow needs a positive ratio — the zero value
					// would silently zero out the port.
					add("tech %q: flows[%d] (%s %s) needs a positive ratio (the reference input flow may omit it)", id, fi, f.Direction, f.Carrier)
				}
			}
			if ins == 0 {
				add("tech %q: flows need at least one input", id)
			}
			if refs > 1 {
				add("tech %q: at most one flow may be the reference input", id)
			}
		}
		if t.Storage != nil && t.Storage.Inflow != nil && !hasSeries(t.Storage.Inflow) {
			add("tech %q: inflow series %q not defined", id, t.Storage.Inflow.SeriesID())
		}

		// Numeric sanity of the capacity/dispatch/storage/cost envelopes. The
		// zero value of a mistyped field must not silently reshape the model.
		validateCapacity(add, "tech "+id, t.Capacity)
		validateOperation(add, "tech "+id, t.Operation)
		validateStorage(add, "tech "+id, t.Storage)
		validateCosts(add, "tech "+id, t.Costs)
		if t.Lifetime != nil && *t.Lifetime <= 0 {
			add("tech %q: lifetime must be positive", id)
		}
		if t.InterestRate != nil && *t.InterestRate < 0 {
			add("tech %q: interest_rate must not be negative", id)
		}
		if t.AnnualOutputMin != nil && *t.AnnualOutputMin < 0 {
			add("tech %q: annual_output_min must not be negative", id)
		}
		if t.AnnualOutputMax != nil && *t.AnnualOutputMax < 0 {
			add("tech %q: annual_output_max must not be negative", id)
		}
		if t.AnnualOutputMin != nil && t.AnnualOutputMax != nil && *t.AnnualOutputMin > *t.AnnualOutputMax {
			add("tech %q: annual_output_min %g exceeds annual_output_max %g", id, *t.AnnualOutputMin, *t.AnnualOutputMax)
		}
		if t.DemandCurtailable && t.Role != RoleDemand {
			add("tech %q: demand_curtailable is only meaningful on role \"demand\"", id)
		}
		// A fraction-of-investment O&M needs an upfront (overnight) investment
		// to be a fraction OF.
		if t.CostBasis == CostAnnualized {
			for _, class := range keysOf(t.Costs) {
				if t.Costs[class].FixedOMFraction != nil {
					add("tech %q: costs.%s.fixed_om_fraction needs the overnight cost basis (cost_basis is annualized)", id, class)
				}
			}
		}

		// Indexed-parameter structure and per-node overrides: every Value
		// must be internally consistent, and every override must name a node
		// the technology is actually placed at.
		for _, v := range techValues(t) {
			if e := v.StructuralError(); e != nil {
				add("tech %q: %v", id, e)
			}
		}
		if e := t.Performance.Validate(); e != nil {
			add("tech %q: %v", id, e)
		}
		if t.Performance != nil && t.Performance.Precomputed != nil && !hasSeries(t.Performance.Precomputed) {
			add("tech %q: performance.precomputed series %q not defined", id, t.Performance.Precomputed.SeriesID())
		}
		for _, nid := range keysOf(t.NodeOverrides) {
			ov := t.NodeOverrides[nid]
			if !ContainsStr(t.Node, nid) {
				add("tech %q: node_override %q is not in the tech's node list", id, nid)
			}
			if !hasNode(nid) {
				add("tech %q: node_override %q is not a defined node", id, nid)
			}
			if e := ov.Performance.Validate(); e != nil {
				add("tech %q override %q: %v", id, nid, e)
			}
			for _, v := range overrideValues(ov) {
				if e := v.StructuralError(); e != nil {
					add("tech %q override %q: %v", id, nid, e)
				}
				if v.IsSeries() && !hasSeries(v) {
					add("tech %q override %q: time-series %q not defined", id, nid, v.SeriesID())
				}
			}
			where := fmt.Sprintf("tech %q override %q", id, nid)
			validateCapacity(add, where, ov.Capacity)
			validateOperation(add, where, ov.Operation)
			validateStorage(add, where, ov.Storage)
			validateCosts(add, where, ov.Costs)
		}
	}

	for _, nid := range keysOf(m.Nodes) {
		n := m.Nodes[nid]
		if n.AvailableArea != nil && *n.AvailableArea < 0 {
			add("node %q: available_area must not be negative", nid)
		}
		for _, col := range keysOf(n.Climate) {
			// Climate columns feed interned series ids, so they follow the
			// same identifier rule as the registry keys they end up in.
			if !validID(col) {
				add("node %q: climate column %q is invalid (allowed: %s)", nid, col, idCharset)
			}
			if v := n.Climate[col]; v.IsSeries() {
				if _, ok := m.Timeseries[v.SeriesID()]; !ok {
					add("node %q: climate series %q (column %s) not defined", nid, v.SeriesID(), col)
				}
			}
		}
	}

	// Constraints and emission limits render into ONE Calliope add_math map, so
	// a name reused anywhere across the two lists would silently drop an entry.
	seenNames := map[string]string{}
	checkName := func(where, name string) {
		if name == "" {
			return
		}
		if first, dup := seenNames[name]; dup {
			add("%s: name %q is already used by %s", where, name, first)
		} else {
			seenNames[name] = where
		}
	}

	for i, c := range m.Constraints {
		checkName(fmt.Sprintf("constraints[%d]", i), c.Name)
		switch c.Sense {
		case "<=", ">=", "==":
		default:
			add("constraints[%d] %q: sense %q must be one of <=, >=, ==", i, c.Name, c.Sense)
		}
		if len(c.Terms) == 0 {
			add("constraints[%d] %q: has no terms", i, c.Name)
		}
		for j, term := range c.Terms {
			if !term.Variable.Valid() {
				add("constraints[%d].terms[%d]: variable %q is invalid", i, j, term.Variable)
			}
			for _, tid := range term.Techs {
				if _, ok := m.Technologies[tid]; !ok {
					add("constraints[%d] %q: tech %q not defined", i, c.Name, tid)
				}
			}
			for _, cc := range term.Carriers {
				if !hasCarrier(cc) {
					add("constraints[%d] %q: carrier %q not defined", i, c.Name, cc)
				}
			}
			for _, nn := range term.Nodes {
				if !hasNode(nn) {
					add("constraints[%d] %q: node %q not defined", i, c.Name, nn)
				}
			}
		}
	}

	for _, id := range keysOf(m.Trade) {
		tr := m.Trade[id]
		if !hasNode(tr.Node) {
			add("trade %q: node %q not defined", id, tr.Node)
		}
		if !hasCarrier(tr.Carrier) {
			add("trade %q: carrier %q not defined", id, tr.Carrier)
		}
		if tr.Import == nil && tr.Export == nil {
			add("trade %q: neither import nor export is defined", id)
		}
		for _, s := range []struct {
			side *TradeSide
			name string
		}{{tr.Import, "import"}, {tr.Export, "export"}} {
			if s.side == nil {
				continue
			}
			if s.side.Price != nil && !hasSeries(s.side.Price) {
				add("trade %q: %s price series %q not defined", id, s.name, s.side.Price.SeriesID())
			}
			if s.side.Limit != nil && !hasSeries(s.side.Limit) {
				add("trade %q: %s limit series %q not defined", id, s.name, s.side.Limit.SeriesID())
			}
			if f, ok := s.side.Limit.Reduce(); ok && f < 0 {
				add("trade %q: %s limit must not be negative", id, s.name)
			}
		}
	}

	for i, el := range m.EmissionLimits {
		checkName(fmt.Sprintf("emission_limits[%d]", i), el.Name)
		switch el.Sense {
		case "<=", ">=", "==":
		default:
			add("emission_limits[%d]: sense %q must be one of <=, >=, ==", i, el.Sense)
		}
	}

	for _, id := range keysOf(m.Transmission) {
		l := m.Transmission[id]
		if !hasCarrier(l.Carrier) {
			add("transmission %q: carrier %q not defined", id, l.Carrier)
		}
		if !hasNode(l.From) {
			add("transmission %q: from-node %q not defined", id, l.From)
		}
		if !hasNode(l.To) {
			add("transmission %q: to-node %q not defined", id, l.To)
		}
		if l.From == l.To && l.From != "" {
			add("transmission %q: from and to are the same node %q", id, l.From)
		}
		if f, ok := l.Efficiency.Reduce(); ok && (f <= 0 || f > 1) {
			add("transmission %q: efficiency must be within (0,1]", id)
		}
		if l.Distance != nil && *l.Distance < 0 {
			add("transmission %q: distance must not be negative", id)
		}
		if l.LossPerDistance != nil {
			if *l.LossPerDistance < 0 {
				add("transmission %q: loss_per_distance must not be negative", id)
			}
			if l.Distance != nil && *l.LossPerDistance**l.Distance >= 1 {
				add("transmission %q: loss_per_distance * distance is %g; the arc would lose its whole flow", id, *l.LossPerDistance**l.Distance)
			}
		}
		if l.MinFlow != nil && (*l.MinFlow < 0 || *l.MinFlow > 1) {
			add("transmission %q: min_flow must be within [0,1] (fraction of arc capacity)", id)
		}
		if ec := l.EnergyConsumption; ec != nil {
			if !hasCarrier(ec.Carrier) {
				add("transmission %q: energy_consumption carrier %q not defined", id, ec.Carrier)
			}
			if ec.PerFlow < 0 || ec.PerFlowDistance < 0 {
				add("transmission %q: energy_consumption rates must not be negative", id)
			}
			if ec.PerFlowDistance > 0 && l.Distance == nil {
				add("transmission %q: energy_consumption.per_flow_distance needs distance", id)
			}
		}
		validateCapacity(add, "transmission "+id, l.Capacity)
		validateCosts(add, "transmission "+id, l.Costs)
	}

	return errors.Join(errs...)
}

// parseDate parses s under one of the ISO 8601 layouts the emit layer accepts
// (kept in sync with emit.ParseDate; duplicated here because emit imports
// model).
func parseDate(s string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05", "2006-01-02 15:04:05",
		"2006-01-02T15:04", "2006-01-02 15:04",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// parseableDate reports whether s parses as an ISO 8601 timestamp.
func parseableDate(s string) bool {
	_, ok := parseDate(s)
	return ok
}

// idCharset documents the identifier rule for error messages.
const idCharset = `letters, digits, ".", "_" and "-", not starting with "."`

// validID reports whether id is safe to use verbatim as a filesystem path
// component. Emitters join these ids into output paths (adoptnet0 writes
// node_data/<node-id>/ directories and carrier_data/<carrier-id>.csv, calliope
// writes <series-id>.csv data tables), so the charset is a conservative
// whitelist rather than per-emitter sanitizing. A leading "." is rejected to
// rule out ".", ".." and hidden files.
func validID(id string) bool {
	if id == "" || id[0] == '.' {
		return false
	}
	for i := 0; i < len(id); i++ {
		switch c := id[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// validSeriesID applies validID to a timeseries registry key, additionally
// accepting the "_inline:<kind>:<id>:<field>" form InternInlineSeries
// generates (validated segment-wise so the colons cannot smuggle separators;
// interning runs before Validate, so its output must pass here).
func validSeriesID(id string) bool {
	if strings.HasPrefix(id, "_inline:") {
		for _, seg := range strings.Split(id, ":") {
			if !validID(seg) {
				return false
			}
		}
		return true
	}
	return validID(id)
}

// --- numeric range checks -----------------------------------------------------
//
// These guard against the silent zero value: a negative or out-of-range number
// in a bound/efficiency slot never reaches an emitter.

func validateCapacity(add func(string, ...any), where string, c *Capacity) {
	if c == nil {
		return
	}
	if c.Existing < 0 {
		add("%s: capacity.existing must not be negative", where)
	}
	if c.Min != nil && *c.Min < 0 {
		add("%s: capacity.min must not be negative", where)
	}
	if c.Max != nil && *c.Max < 0 {
		add("%s: capacity.max must not be negative", where)
	}
	if c.Min != nil && c.Max != nil && *c.Min > *c.Max {
		add("%s: capacity.min %g exceeds capacity.max %g", where, *c.Min, *c.Max)
	}
	if c.PerUnit != nil && *c.PerUnit <= 0 {
		add("%s: capacity.per_unit must be positive", where)
	}
	if (c.UnitsMin != nil || c.UnitsMax != nil) && c.PerUnit == nil {
		add("%s: capacity.units_min/units_max need capacity.per_unit (the module size)", where)
	}
	if c.UnitsMin != nil && *c.UnitsMin < 0 {
		add("%s: capacity.units_min must not be negative", where)
	}
	if c.UnitsMax != nil && *c.UnitsMax < 0 {
		add("%s: capacity.units_max must not be negative", where)
	}
	if c.UnitsMin != nil && c.UnitsMax != nil && *c.UnitsMin > *c.UnitsMax {
		add("%s: capacity.units_min %d exceeds units_max %d", where, *c.UnitsMin, *c.UnitsMax)
	}
	if c.SystemwideMin != nil && *c.SystemwideMin < 0 {
		add("%s: capacity.systemwide_min must not be negative", where)
	}
	if c.SystemwideMax != nil && *c.SystemwideMax < 0 {
		add("%s: capacity.systemwide_max must not be negative", where)
	}
	if c.SystemwideMin != nil && c.SystemwideMax != nil && *c.SystemwideMin > *c.SystemwideMax {
		add("%s: capacity.systemwide_min %g exceeds systemwide_max %g", where, *c.SystemwideMin, *c.SystemwideMax)
	}
	if d := c.Decommission; d != nil {
		switch d.Mode {
		case "", "impossible", "continuous", "only_complete":
		default:
			add("%s: capacity.decommission.mode %q is invalid (impossible, continuous or only_complete)", where, d.Mode)
		}
		if d.Cost != nil && *d.Cost < 0 {
			add("%s: capacity.decommission.cost must not be negative", where)
		}
	}
}

func validateOperation(add func(string, ...any), where string, op *Operation) {
	if op == nil {
		return
	}
	if f, ok := op.RampUp.Reduce(); ok && f < 0 {
		add("%s: operation.ramp_up must not be negative", where)
	}
	if f, ok := op.RampDown.Reduce(); ok && f < 0 {
		add("%s: operation.ramp_down must not be negative", where)
	}
	if op.StartUpCost != nil && *op.StartUpCost < 0 {
		add("%s: operation.start_up_cost must not be negative", where)
	}
	if op.ShutDownCost != nil && *op.ShutDownCost < 0 {
		add("%s: operation.shut_down_cost must not be negative", where)
	}
	if op.MinUptime != nil && *op.MinUptime < 0 {
		add("%s: operation.min_uptime must not be negative", where)
	}
	if op.MinDowntime != nil && *op.MinDowntime < 0 {
		add("%s: operation.min_downtime must not be negative", where)
	}
	minPU, minOK := op.MinPU.Reduce()
	maxPU, maxOK := op.MaxPU.Reduce()
	if minOK && minPU < 0 {
		add("%s: operation.min_pu must not be negative", where)
	}
	if maxOK && maxPU < 0 {
		add("%s: operation.max_pu must not be negative", where)
	}
	if minOK && maxOK && minPU > maxPU {
		add("%s: operation.min_pu %g exceeds max_pu %g", where, minPU, maxPU)
	}
	if f, ok := op.EqualsPU.Reduce(); ok && f < 0 {
		add("%s: operation.equals_pu must not be negative", where)
	}
	if op.MaxStartups != nil && *op.MaxStartups < 0 {
		add("%s: operation.max_startups must not be negative", where)
	}
	if op.StandbyPower != nil && (*op.StandbyPower < 0 || *op.StandbyPower > 1) {
		add("%s: operation.standby_power must be within [0,1] (fraction of capacity)", where)
	}
}

func validateStorage(add func(string, ...any), where string, s *Storage) {
	if s == nil {
		return
	}
	frac := func(name string, v *float64, min, max float64) {
		if v != nil && (*v < min || *v > max) {
			add("%s: storage.%s must be within [%g,%g]", where, name, min, max)
		}
	}
	if s.ChargeEff != nil && (*s.ChargeEff <= 0 || *s.ChargeEff > 1) {
		add("%s: storage.charge_eff must be within (0,1]", where)
	}
	if s.DischargeEff != nil && (*s.DischargeEff <= 0 || *s.DischargeEff > 1) {
		add("%s: storage.discharge_eff must be within (0,1]", where)
	}
	frac("self_discharge", s.SelfDischarge, 0, 1)
	frac("initial_soc", s.InitialSOC, 0, 1)
	frac("depth_of_discharge", s.DepthOfDischarge, 0, 1)
	if s.MaxHours != nil && *s.MaxHours <= 0 {
		add("%s: storage.max_hours must be positive", where)
	}
	if s.SpillCost != nil && *s.SpillCost < 0 {
		add("%s: storage.spill_cost must not be negative", where)
	}
	if s.MaxChargeRate != nil && *s.MaxChargeRate <= 0 {
		add("%s: storage.max_charge_rate must be positive", where)
	}
	if s.MaxDischargeRate != nil && *s.MaxDischargeRate <= 0 {
		add("%s: storage.max_discharge_rate must be positive", where)
	}
	// MaxHours fixes the power/energy ratio; the rate bounds size them
	// independently. Both at once contradict each other.
	if s.MaxHours != nil && (s.MaxChargeRate != nil || s.MaxDischargeRate != nil) {
		add("%s: storage.max_hours (fixed power/energy ratio) cannot be combined with max_charge_rate/max_discharge_rate (independent sizing)", where)
	}
	validateCapacity(add, where+" energy_capacity", s.EnergyCapacity)
}

func validateCosts(add func(string, ...any), where string, costs map[string]CostClass) {
	for _, class := range keysOf(costs) {
		// Cost-class keys become segments of interned series ids (and thus of
		// emitted file names), so they follow the identifier rule too.
		if !validID(class) {
			add("%s: cost class %q is invalid (allowed: %s)", where, class, idCharset)
		}
		c := costs[class]
		nonNeg := func(name string, v *float64) {
			if v != nil && *v < 0 {
				add("%s: costs.%s.%s must not be negative", where, class, name)
			}
		}
		nonNeg("investment_per_capacity", c.InvestmentPerCapacity)
		nonNeg("investment_per_energy_capacity", c.InvestmentPerEnergyCapacity)
		nonNeg("investment_per_capacity_distance", c.InvestmentPerCapacityDistance)
		nonNeg("fixed_om", c.FixedOM)
		nonNeg("fixed_om_fraction", c.FixedOMFraction)
		nonNeg("purchase", c.Purchase)
		if c.FixedOM != nil && c.FixedOMFraction != nil {
			add("%s: costs.%s: fixed_om (absolute) and fixed_om_fraction (fraction of investment) are mutually exclusive", where, class)
		}
		// VariableOM and FuelCost may legitimately be negative (subsidies).
	}
}

// --- Value-walk helpers -------------------------------------------------------
//
// techValues/overrideValues enumerate every *Value slot of a technology (and
// its per-node overrides) so validation checks each exactly once. Keep them in
// sync with Model.InternInlineSeries, which walks the same slots.

func techValues(t Technology) []*Value {
	vs := []*Value{t.Efficiency, t.DemandProfile}
	if t.Operation != nil {
		vs = append(vs, t.Operation.MaxPU, t.Operation.MinPU, t.Operation.EqualsPU)
	}
	if t.Storage != nil {
		vs = append(vs, t.Storage.Inflow)
	}
	if t.Source != nil {
		vs = append(vs, t.Source.Max)
	}
	for _, c := range t.Costs {
		vs = append(vs, c.VariableOM, c.FuelCost)
	}
	return nonNilValues(vs)
}

func overrideValues(ov NodeOverride) []*Value {
	vs := []*Value{ov.Efficiency, ov.DemandProfile}
	if ov.Operation != nil {
		vs = append(vs, ov.Operation.MaxPU, ov.Operation.MinPU, ov.Operation.EqualsPU)
	}
	if ov.Storage != nil {
		vs = append(vs, ov.Storage.Inflow)
	}
	if ov.Source != nil {
		vs = append(vs, ov.Source.Max)
	}
	for _, c := range ov.Costs {
		vs = append(vs, c.VariableOM, c.FuelCost)
	}
	return nonNilValues(vs)
}

func nonNilValues(vs []*Value) []*Value {
	out := vs[:0]
	for _, v := range vs {
		if v != nil {
			out = append(out, v)
		}
	}
	return out
}

// FirstMultiIndexed finds the first indexed parameter that carries more than one
// entry (so it cannot be reduced to a scalar for a flat target).
func FirstMultiIndexed(m *Model) (where string, ok bool) {
	for _, id := range keysOf(m.Technologies) {
		t := m.Technologies[id]
		for _, v := range techValues(t) {
			if v.IsIndexed() && len(v.Indexed.Data) > 1 {
				return "tech " + id, true
			}
		}
		for _, nid := range keysOf(t.NodeOverrides) {
			ov := t.NodeOverrides[nid]
			for _, v := range overrideValues(ov) {
				if v.IsIndexed() && len(v.Indexed.Data) > 1 {
					return "tech " + id + " override " + nid, true
				}
			}
		}
	}
	return "", false
}

// ContainsStr reports whether s contains v.
func ContainsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// NativeWarnings reports native blocks that will be ignored when emitting for
// `target` (they belong to another framework) and flags loss of portability
// when the selected target's own block is used. allTargets is the registry's
// list of known target names.
func NativeWarnings(target Target, j *Job, allTargets []string) []string {
	var w []string
	locked := false
	check := func(what string, n *Native) {
		if n == nil {
			return
		}
		if len(n.For(target)) > 0 {
			locked = true
		}
		for _, other := range allTargets {
			if Target(other) != target && len(n.For(Target(other))) > 0 {
				w = append(w, fmt.Sprintf("%s: native.%s block is ignored when emitting for %q", what, other, target))
			}
		}
	}

	m := &j.Model
	check("model", m.Native)
	check("experiment", j.Experiment.Native)
	for _, id := range keysOf(m.Technologies) {
		check("tech "+id, m.Technologies[id].Native)
	}
	for _, id := range keysOf(m.Transmission) {
		check("transmission "+id, m.Transmission[id].Native)
	}
	for _, id := range keysOf(m.Trade) {
		check("trade "+id, m.Trade[id].Native)
	}
	for _, id := range keysOf(m.Nodes) {
		check("node "+id, m.Nodes[id].Native)
	}
	for _, id := range keysOf(m.Carriers) {
		check("carrier "+id, m.Carriers[id].Native)
	}
	if locked {
		w = append(w, fmt.Sprintf("payload uses native.%s overrides — the emitted model is specific to %q and not portable to other targets", target, target))
	}
	return w
}
