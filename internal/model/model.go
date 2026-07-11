// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package model defines a framework-neutral canonical representation of an
// energy-system optimization model: the domain types (technologies, nodes,
// carriers, flows, constraints, performance curves), their structural
// validation, and the capability matrix describing which target simulators can
// honor which features.
//
// The REST payload is a Job = Model (the physical system) + Experiment (the run
// spec). A handler unmarshals it and calls Job.ValidateFor(target); the emit
// package then translates it into a target's native input format. This package
// depends on nothing else in the module, so it is the shared vocabulary that
// the emit and service layers build on.
package model

// Model is the physical system: its time domain, carriers, nodes, technologies,
// transmission, market trade, and system-wide emission limits. Run/experiment
// concerns live on Experiment. Collections are keyed by a stable identifier.
type Model struct {
	Metadata Metadata   `json:"metadata"`
	Time     TimeConfig `json:"time"`
	Periods  []Period   `json:"periods,omitempty"` // optional multi-horizon (name or rich object)
	// DiscountRate is a global discount/interest rate (fraction) that, when
	// set, overrides every technology's own interest_rate for annualizing
	// investment (AdOpT global_discountrate; broadcast for PyPSA/Calliope).
	DiscountRate   *float64                `json:"discount_rate,omitempty"`
	Carriers       map[string]Carrier      `json:"carriers"`
	Nodes          map[string]Node         `json:"nodes"`
	Timeseries     map[string]TimeSeries   `json:"timeseries,omitempty"`
	Technologies   map[string]Technology   `json:"technologies"`
	Transmission   map[string]Transmission `json:"transmission,omitempty"`
	Trade          map[string]Trade        `json:"trade,omitempty"`           // per-(node,carrier) import/export
	EmissionLimits []EmissionLimit         `json:"emission_limits,omitempty"` // system-wide caps
	Constraints    []Constraint            `json:"constraints,omitempty"`     // portable linear constraints
	Native         *Native                 `json:"native,omitempty"`          // system-wide target-specific config
}

// EffectiveRate resolves the interest rate used to annualize a technology's
// investment: the global discount rate wins over the component rate.
func (m *Model) EffectiveRate(t Technology) *float64 {
	if m.DiscountRate != nil {
		return m.DiscountRate
	}
	return t.InterestRate
}

// Metadata identifies and annotates a model. Name is required; Currency and
// CurrencyYear document the unit and reference year of every cost figure (no
// framework converts currencies).
type Metadata struct {
	Name         string `json:"name"`
	Version      string `json:"version,omitempty"`
	Description  string `json:"description,omitempty"`
	Currency     string `json:"currency,omitempty"`
	CurrencyYear int    `json:"currency_year,omitempty"`
}

// TimeConfig defines the snapshot/timestep index of the system. Temporal
// *aggregation* (clustering) is a run concern and lives on Experiment.
type TimeConfig struct {
	Start      string   `json:"start"`                // ISO8601
	End        string   `json:"end"`                  // ISO8601
	Resolution string   `json:"resolution,omitempty"` // e.g. "1H", "3H"
	Subset     []string `json:"subset,omitempty"`     // [start, end] inclusive
	// Timesteps are explicit timestep labels for irregular indices; when set
	// they take precedence over start/end/resolution for the emitted time
	// index (PyPSA snapshots, Calliope data tables). AdOpT-NET0 needs a
	// regular pandas date_range and rejects them.
	Timesteps []string `json:"timesteps,omitempty"`
	// Weights is the objective/accounting weight of each timestep (hours
	// represented; scalar or series). PyPSA snapshot_weightings; Calliope
	// timestep_weights (scalar only); rejected by AdOpT-NET0.
	Weights *Value `json:"weights,omitempty"`
}

// Carrier is an energy or material vector (electricity, hydrogen, heat, CO2...).
// Kept orthogonal to Node; the PyPSA emitter fuses the two into buses.
type Carrier struct {
	Name         string   `json:"name,omitempty"`
	Unit         string   `json:"unit,omitempty"`
	CO2Intensity *float64 `json:"co2_intensity,omitempty"`
	Color        string   `json:"color,omitempty"`
	Native       *Native  `json:"native,omitempty"`
}

// Node is a spatial point. Technologies live at a node and declare their
// carriers; a node never carries carrier identity itself.
type Node struct {
	Name          string           `json:"name,omitempty"`
	Coords        *Coords          `json:"coords,omitempty"`
	AllowedTechs  []string         `json:"allowed_techs,omitempty"`
	AvailableArea *float64         `json:"available_area,omitempty"` // Calliope area budget at this node
	Climate       map[string]Value `json:"climate,omitempty"`        // AdOpT weather series (ghi, dni, dhi, temp_air, rh, ws10, hydro_inflow); scalar or timeseries ref
	Native        *Native          `json:"native,omitempty"`
}

// Coords is a WGS84 position; Alt is metres above sea level (consumed by
// AdOpT-NET0's climate preprocessing).
type Coords struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
	Alt float64 `json:"alt,omitempty"`
}

// TimeSeries is either an inline vector or a reference to a CSV column on disk.
type TimeSeries struct {
	Source string    `json:"source"`           // "inline" | "file"
	Path   string    `json:"path,omitempty"`   // for source=="file"
	Column string    `json:"column,omitempty"` // for source=="file"
	Values []float64 `json:"values,omitempty"` // for source=="inline"
	Unit   string    `json:"unit,omitempty"`   // informational (e.g. MW, fraction)
}

// Technology is any node-local component that produces, consumes, converts, or
// stores carriers. A tech may sit at several nodes (Calliope-style); emitters
// expand it to one instance per node.
type Technology struct {
	Role       Role       `json:"role"`
	Node       StringList `json:"node"`
	CarrierIn  StringList `json:"carrier_in,omitempty"`
	CarrierOut StringList `json:"carrier_out,omitempty"`
	// Efficiency is the simple one-in/one-out conversion factor. For multi-input
	// or multi-output conversion (CHP, electrolysis with byproducts, sector
	// coupling), use Flows instead; when Flows is non-empty it takes precedence.
	Efficiency    *Value               `json:"efficiency,omitempty"`
	Flows         []Flow               `json:"flows,omitempty"`
	Performance   *Performance         `json:"performance,omitempty"`
	Capacity      *Capacity            `json:"capacity,omitempty"`
	Operation     *Operation           `json:"operation,omitempty"`
	Storage       *Storage             `json:"storage,omitempty"`
	DemandProfile *Value               `json:"demand_profile,omitempty"`
	Area          *AreaSpec            `json:"area,omitempty"`   // Calliope area-limited siting
	Source        *SourceSpec          `json:"source,omitempty"` // Calliope resource/source bounds
	Costs         map[string]CostClass `json:"costs,omitempty"`
	Lifetime      *float64             `json:"lifetime,omitempty"`
	InterestRate  *float64             `json:"interest_rate,omitempty"`
	CostBasis     CostBasis            `json:"cost_basis,omitempty"`
	// EmissionFactor is the direct emission per MWh of reference output
	// (t CO2-eq/MWh; negative for negative-emission technologies). AdOpT
	// Performance.emission_factor; Calliope co2 cost class on flow_out;
	// rejected by PyPSA (its emission accounting is carrier-wide).
	EmissionFactor *float64 `json:"emission_factor,omitempty"`
	// AnnualOutputMin/Max bound the total reference output over the horizon
	// (MWh). PyPSA e_sum_min/e_sum_max on supply; rejected elsewhere.
	AnnualOutputMin *float64 `json:"annual_output_min,omitempty"`
	AnnualOutputMax *float64 `json:"annual_output_max,omitempty"`
	// BuildYear is the commissioning year of existing capacity (PyPSA
	// build_year; informational elsewhere - emitters warn).
	BuildYear *int `json:"build_year,omitempty"`
	// Active deactivates the component without deleting it (nil = active).
	Active *bool `json:"active,omitempty"`
	// DemandCurtailable turns the demand profile into an upper bound instead
	// of an equality (Calliope sink_use_max; rejected elsewhere).
	DemandCurtailable bool                    `json:"demand_curtailable,omitempty"`
	NodeOverrides     map[string]NodeOverride `json:"node_overrides,omitempty"` // per-node parameter overrides
	Native            *Native                 `json:"native,omitempty"`
}

// IsActive reports whether the technology takes part in the model (Active nil
// or true).
func (t *Technology) IsActive() bool { return t.Active == nil || *t.Active }

// NodeOverride replaces a subset of a technology's parameters at a specific
// node (Calliope node-level overrides / AdOpT per-node technology data). Any
// nil field leaves the base value unchanged; Costs are merged per cost class.
type NodeOverride struct {
	CarrierIn     StringList           `json:"carrier_in,omitempty"`
	CarrierOut    StringList           `json:"carrier_out,omitempty"`
	Efficiency    *Value               `json:"efficiency,omitempty"`
	Performance   *Performance         `json:"performance,omitempty"`
	Capacity      *Capacity            `json:"capacity,omitempty"`
	Operation     *Operation           `json:"operation,omitempty"`
	Storage       *Storage             `json:"storage,omitempty"`
	DemandProfile *Value               `json:"demand_profile,omitempty"`
	Area          *AreaSpec            `json:"area,omitempty"`
	Source        *SourceSpec          `json:"source,omitempty"`
	Costs         map[string]CostClass `json:"costs,omitempty"`
	Native        *Native              `json:"native,omitempty"`
}

// Flow is one port of a multi-carrier conversion. Ratios are per unit of the
// reference input flow (the one marked Reference, or the first input). Maps to a
// PyPSA multi-output Link (bus0 + bus1/efficiency + bus2/efficiency2 + ...),
// Calliope indexed flow_out_eff, or an AdOpT multi-carrier conversion.
type Flow struct {
	Carrier   string  `json:"carrier"`
	Direction FlowDir `json:"direction"` // "in" | "out"
	Ratio     float64 `json:"ratio"`     // per unit of the reference input
	Reference bool    `json:"reference,omitempty"`
}

// FlowDir is the direction of a conversion port relative to the technology.
type FlowDir string

const (
	FlowIn  FlowDir = "in"
	FlowOut FlowDir = "out"
)

// Capacity normalizes fixed base (Existing), whether size is a decision variable
// (Expandable), and bounds. Covers AdOpT existing/new, PyPSA p_nom/extendable,
// Calliope flow_cap_equals/flow_cap_max.
type Capacity struct {
	Existing   float64  `json:"existing"`
	Expandable bool     `json:"expandable"`
	Min        *float64 `json:"min,omitempty"`
	Max        *float64 `json:"max,omitempty"`
	Unit       string   `json:"unit,omitempty"`
	PerUnit    *float64 `json:"per_unit,omitempty"`
	// UnitsMin/UnitsMax bound the number of integer modules (with PerUnit);
	// where Min/Max are absent, emitters use units * per_unit instead.
	UnitsMin *int `json:"units_min,omitempty"`
	UnitsMax *int `json:"units_max,omitempty"`
	// SystemwideMin/SystemwideMax bound this component's capacity summed over
	// all nodes (Calliope flow_cap_*_systemwide; PyPSA sidecar constraint;
	// rejected by AdOpT-NET0).
	SystemwideMin *float64 `json:"systemwide_min,omitempty"`
	SystemwideMax *float64 `json:"systemwide_max,omitempty"`
	// Decommission controls what may happen to Existing capacity.
	Decommission *Decommission `json:"decommission,omitempty"`
}

// Decommission describes the fate of existing capacity during optimization.
// "impossible" (default) fixes it; "continuous" allows partial retirement;
// "only_complete" allows full retirement only (AdOpT-native; rejected
// elsewhere).
type Decommission struct {
	Mode string   `json:"mode,omitempty"` // impossible | continuous | only_complete
	Cost *float64 `json:"cost,omitempty"` // per unit of retired capacity
}

// EffectiveMin resolves the lower capacity bound: Min, else units_min * per_unit.
func (c *Capacity) EffectiveMin() *float64 {
	if c == nil {
		return nil
	}
	if c.Min != nil {
		return c.Min
	}
	if c.UnitsMin != nil && c.PerUnit != nil {
		v := float64(*c.UnitsMin) * *c.PerUnit
		return &v
	}
	return nil
}

// EffectiveMax resolves the upper capacity bound: Max, else units_max * per_unit.
func (c *Capacity) EffectiveMax() *float64 {
	if c == nil {
		return nil
	}
	if c.Max != nil {
		return c.Max
	}
	if c.UnitsMax != nil && c.PerUnit != nil {
		v := float64(*c.UnitsMax) * *c.PerUnit
		return &v
	}
	return nil
}

// DecommissionMode returns the effective decommission behaviour (default
// "impossible").
func (c *Capacity) DecommissionMode() string {
	if c == nil || c.Decommission == nil || c.Decommission.Mode == "" {
		return "impossible"
	}
	return c.Decommission.Mode
}

// Operation is the dispatch envelope, including unit commitment.
type Operation struct {
	MaxPU *Value `json:"max_pu,omitempty"`
	MinPU *Value `json:"min_pu,omitempty"`
	// EqualsPU fixes the dispatch per unit of capacity (a non-curtailable
	// resource); overrides MaxPU/MinPU. PyPSA p_min_pu = p_max_pu; Calliope
	// source_use_equals (supply); rejected by AdOpT-NET0.
	EqualsPU *Value `json:"equals_pu,omitempty"`
	// Ramp limits accept the Value shapes for schema parity, but every target
	// renders them as scalars today (ValidateFor rejects series).
	RampUp       *Value   `json:"ramp_up,omitempty"`
	RampDown     *Value   `json:"ramp_down,omitempty"`
	Committable  bool     `json:"committable,omitempty"`
	StartUpCost  *float64 `json:"start_up_cost,omitempty"`
	ShutDownCost *float64 `json:"shut_down_cost,omitempty"`
	MinUptime    *float64 `json:"min_uptime,omitempty"`
	MinDowntime  *float64 `json:"min_downtime,omitempty"`
	// MaxStartups bounds the number of start-ups over the horizon (AdOpT
	// Performance.max_startups; rejected elsewhere).
	MaxStartups *float64 `json:"max_startups,omitempty"`
	// StandbyPower is the consumption while committed but idle, as a fraction
	// of capacity (AdOpT Performance.standby_power; rejected elsewhere).
	StandbyPower *float64 `json:"standby_power,omitempty"`
}

// Storage is the state-of-charge model of a storage technology. The
// technology's Capacity bounds charge/discharge POWER (MW); EnergyCapacity
// bounds stored ENERGY (MWh). Set MaxHours for a fixed power/energy ratio or
// the rate bounds for independent sizing - the two are mutually exclusive.
type Storage struct {
	EnergyCapacity *Capacity `json:"energy_capacity,omitempty"`
	MaxHours       *float64  `json:"max_hours,omitempty"`
	ChargeEff      *float64  `json:"charge_eff,omitempty"`
	DischargeEff   *float64  `json:"discharge_eff,omitempty"`
	SelfDischarge  *float64  `json:"self_discharge,omitempty"` // -> PyPSA standing_loss
	Inflow         *Value    `json:"inflow,omitempty"`         // hydro inflow (scalar or series)
	SpillCost      *float64  `json:"spill_cost,omitempty"`
	InitialSOC     *float64  `json:"initial_soc,omitempty"`
	Cyclic         bool      `json:"cyclic,omitempty"`
	// Independent power/energy sizing: maximum charge/discharge power as a
	// fraction of energy capacity per hour (AdOpT Flexibility charge_rate /
	// discharge_rate, Calliope flow_cap_per_storage_cap_max). MaxHours covers
	// the fixed-ratio case instead; setting both is rejected.
	MaxChargeRate    *float64 `json:"max_charge_rate,omitempty"`
	MaxDischargeRate *float64 `json:"max_discharge_rate,omitempty"`
	// DepthOfDischarge is the minimum state of charge as a fraction of energy
	// capacity (Calliope storage_discharge_depth).
	DepthOfDischarge *float64 `json:"depth_of_discharge,omitempty"`
	// NoSimultaneousChargeDischarge forbids charging and discharging in the
	// same timestep (binary formulation; Calliope force_async_flow, AdOpT
	// allow_only_one_direction).
	NoSimultaneousChargeDischarge bool `json:"no_simultaneous_charge_discharge,omitempty"`
}

// AreaSpec constrains land/area use (Calliope). No PyPSA/AdOpT analogue, so it is
// capability-gated to Calliope.
type AreaSpec struct {
	Max         *float64 `json:"max,omitempty"`
	PerCapacity *float64 `json:"per_capacity,omitempty"`
}

// SourceSpec bounds a supply technology's resource/source (Calliope). Also
// Calliope-only.
type SourceSpec struct {
	Cap  *float64 `json:"cap,omitempty"`
	Unit string   `json:"unit,omitempty"` // per_area | per_cap | absolute
	Max  *Value   `json:"max,omitempty"`
}

// PrimaryCostClass is the cost-class key every emitter reads to populate the
// framework's monetary cost fields (PyPSA capital_cost/marginal_cost, Calliope
// cost_flow_cap, AdOpT economics). Costs stored under any other class name are
// silently dropped by the emitters — ValidateFor warns about that.
const PrimaryCostClass = "monetary"

// CostClass generalizes costs across the three frameworks' cost models.
type CostClass struct {
	InvestmentPerCapacity       *float64 `json:"investment_per_capacity,omitempty"`
	InvestmentPerEnergyCapacity *float64 `json:"investment_per_energy_capacity,omitempty"`
	// InvestmentPerCapacityDistance is a transmission-only investment cost per
	// unit capacity AND km (Calliope cost_flow_cap_per_distance; AdOpT gamma4;
	// warned-and-dropped by PyPSA until transmission costs are wired).
	InvestmentPerCapacityDistance *float64 `json:"investment_per_capacity_distance,omitempty"`
	FixedOM                       *float64 `json:"fixed_om,omitempty"`
	// FixedOMFraction is the fixed annual O&M as a fraction of the upfront
	// investment (AdOpT's OPEX_fixed convention; Calliope
	// cost_om_annual_investment_fraction; PyPSA folds fraction * investment
	// into capital_cost). Mutually exclusive with FixedOM and requires the
	// overnight cost basis.
	FixedOMFraction *float64 `json:"fixed_om_fraction,omitempty"`
	VariableOM      *Value   `json:"variable_om,omitempty"`
	// FuelCost prices the carrier INPUT per MWh, distinct from VariableOM on
	// output (Calliope cost_flow_in; PyPSA folds fuel/efficiency into
	// marginal_cost; rejected by AdOpT-NET0).
	FuelCost *Value   `json:"fuel_cost,omitempty"`
	Purchase *float64 `json:"purchase,omitempty"`
}

// Transmission moves one carrier between two nodes.
type Transmission struct {
	Carrier       string    `json:"carrier"`
	From          string    `json:"from"`
	To            string    `json:"to"`
	Bidirectional bool      `json:"bidirectional"`
	Capacity      *Capacity `json:"capacity,omitempty"`
	// Efficiency accepts the Value shapes for schema parity; every target
	// renders it as a scalar today (ValidateFor rejects series).
	Efficiency *Value   `json:"efficiency,omitempty"`
	Distance   *float64 `json:"distance,omitempty"`
	// LossPerDistance is the transport loss per km as a fraction of flow
	// (AdOpT network loss; Calliope 1 - flow_out_eff_per_distance; folded into
	// the PyPSA link efficiency over Distance). Requires Distance to take
	// effect and (1 - loss*distance) must stay positive.
	LossPerDistance *float64 `json:"loss_per_distance,omitempty"`
	// MinFlow forces a minimum flow while the arc is used, as a fraction of
	// arc capacity (AdOpT min_transport; Calliope flow_out_min_relative; PyPSA
	// p_min_pu on one-way links).
	MinFlow *float64 `json:"min_flow,omitempty"`
	// EmissionFactor is the emissions per MWh transported (t CO2-eq/MWh;
	// Calliope co2 cost class on the link tech; rejected elsewhere).
	EmissionFactor *float64 `json:"emission_factor,omitempty"`
	// EnergyConsumption is auxiliary energy consumed by transporting (e.g.
	// compressor electricity), linear in flow and flow*distance (PyPSA
	// multi-port link withdrawing from the aux bus; rejected elsewhere).
	EnergyConsumption *TransportEnergy     `json:"energy_consumption,omitempty"`
	Costs             map[string]CostClass `json:"costs,omitempty"`
	// Active deactivates the arc without deleting it (nil = active).
	Active *bool   `json:"active,omitempty"`
	Native *Native `json:"native,omitempty"`
}

// IsActive reports whether the arc takes part in the model.
func (l *Transmission) IsActive() bool { return l.Active == nil || *l.Active }

// TransportEnergy is the linear auxiliary-consumption model of a transmission
// arc: consumed carrier units per MWh transported, plus per MWh and km.
type TransportEnergy struct {
	Carrier         string  `json:"carrier"`
	PerFlow         float64 `json:"per_flow,omitempty"`
	PerFlowDistance float64 `json:"per_flow_distance,omitempty"`
}

// Trade is import/export of a carrier at a node against an external market.
// PyPSA models it as generators with marginal_cost = price; Calliope as
// supply/demand with flow_export; AdOpT as carrier_data import/export columns.
type Trade struct {
	Node    string     `json:"node"`
	Carrier string     `json:"carrier"`
	Import  *TradeSide `json:"import,omitempty"`
	Export  *TradeSide `json:"export,omitempty"`
	Native  *Native    `json:"native,omitempty"`
}

// TradeSide is one direction of market exchange; Limit and Price apply per
// timestep.
type TradeSide struct {
	Limit          *Value   `json:"limit,omitempty"`           // MW cap (scalar or series)
	Price          *Value   `json:"price,omitempty"`           // per MWh (scalar or series)
	EmissionFactor *float64 `json:"emission_factor,omitempty"` // per MWh exchanged (import and export)
}

// EmissionLimit is a system-wide cap on an emission species. Maps to a PyPSA
// GlobalConstraint (type primary_energy), a Calliope cost_max on a cost class,
// or an AdOpT emission target.
type EmissionLimit struct {
	Name    string  `json:"name,omitempty"`
	Carrier string  `json:"carrier,omitempty"` // emission attribute, default co2_emissions
	Sense   string  `json:"sense"`             // "<=" | ">=" | "=="
	Limit   float64 `json:"limit"`
	Period  string  `json:"period,omitempty"`
}

// Performance describes how a technology converts input to output beyond a
// single efficiency: a piecewise part-load curve, or a physics-based model
// (pvlib PV, wind power curve, heat-pump COP, run-of-river hydro). AdOpT-NET0
// evaluates these natively; PyPSA and Calliope require a precomputed series
// (e.g. run pvlib externally to get a capacity-factor profile).
type Performance struct {
	Type PerfType `json:"type"` // constant | piecewise | physics

	// type == constant
	Efficiency *Value `json:"efficiency,omitempty"`

	// type == piecewise (part-load performance -> MILP with min stable load)
	Breakpoints []Breakpoint `json:"breakpoints,omitempty"`
	MinLoad     *float64     `json:"min_load,omitempty"` // minimum stable load, fraction of capacity

	// type == physics
	Model       string         `json:"model,omitempty"`       // pv | wind | heat_pump | open_hydro
	Params      map[string]any `json:"params,omitempty"`      // model-specific inputs (tilt, azimuth, cop_ref, ...)
	Precomputed *Value         `json:"precomputed,omitempty"` // series id: profile for non-AdOpT targets
}

// Breakpoint is one point of a piecewise part-load performance curve.
type Breakpoint struct {
	Load       float64 `json:"load"`       // fraction of capacity, 0..1
	Efficiency float64 `json:"efficiency"` // efficiency at that load
}

// PerfType selects a technology's performance formulation.
type PerfType string

const (
	PerfConstant  PerfType = "constant"
	PerfPiecewise PerfType = "piecewise"
	PerfPhysics   PerfType = "physics"
)
