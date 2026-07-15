# Capability Reference

What the canonical JSON schema can express, how each framework's `native`
passthrough works, and — property by property — what each target actually
honors. This document mirrors the enforced behavior:

- **✓ emitted** — the property lands in the target's native input and affects
  the solve. Every ✓ is backed by a runnable scenario
  ([`internal/scenarios/testdata/scenarios/`](../internal/scenarios/testdata/scenarios/)); the
  build fails if a claimed capability loses its scenario (`TestCapabilityCoverage`).
- **✗ rejected** — `ValidateFor` returns 422 with a pointed message. A payload
  is never accepted and then partially ignored.
- **⚠ warning** — accepted, but the property is dropped and a warning names it
  in the response (`warnings: [...]`).
- **· informational** — accepted metadata with no solver effect on any target.

Targets: **P** = PyPSA 1.2.4 · **C** = Calliope 0.7.0.dev7 · **A** = AdOpT-NET0 0.1.10
(pinned in [`environment/requirements*.txt`](../environment/)).

---

## 1. Shared canonical schema

One `Job` = `model` (the physical system) + `experiment` (how to run it).

```jsonc
{
  "model": {
    "metadata":  { "name": "…", "version": "…", "description": "…", "currency": "EUR", "currency_year": 2025 },
    "time":      { "start": "2025-01-01", "end": "2025-01-02", "resolution": "1H", "subset": ["…", "…"],
                   "timesteps": ["…", …],        // explicit labels (P/C; A needs a regular range)
                   "weights": 1 | "ts-id" },     // snapshot weightings (P; C scalar; A rejects)
    "periods":   ["…" | {"name":"…","year":2030,"length_years":5,"objective_weight":1}],  // reserved for multi-horizon
    "discount_rate": 0.07,                       // global; overrides per-tech interest_rate
    "carriers":  { "<id>": { "unit": "MWh", "co2_intensity": 0.2, "color": "…", "native": { … } } },
    "nodes":     { "<id>": { "coords": {"lat":48,"lon":11,"alt":0}, "available_area": 1e4,
                             "climate": { "ghi": "ts-id", "temp_air": 8.5, … }, "native": { … } } },
    "timeseries":{ "<id>": { "source": "inline", "values": [ … ], "unit": "MW" }   // or
                            { "source": "file", "path": "…", "column": "…" } },
    "technologies": {
      "<id>": {
        "role": "supply" | "demand" | "conversion" | "storage",
        "node": "n1" | ["n1","n2"],
        "carrier_in": "…" | ["…"],  "carrier_out": "…" | ["…"],
        "efficiency": 0.9,                        // simple 1-in/1-out factor
        "flows": [                                // multi-port conversion (takes precedence)
          { "carrier": "gas",  "direction": "in",  "reference": true },
          { "carrier": "heat", "direction": "out", "ratio": 0.45 }      // per unit of reference input
        ],
        "performance": {                          // beyond a single efficiency
          "type": "constant" | "piecewise" | "physics",
          "efficiency": 0.6,                                            // constant
          "breakpoints": [{"load":0.3,"efficiency":0.4}, …], "min_load": 0.3,  // piecewise
          "model": "pv" | "wind" | "heat_pump" | "open_hydro",          // physics
          "params": { … }, "precomputed": "ts-id"                       // profile for non-AdOpT targets
        },
        "capacity":  { "existing": 100, "expandable": true, "min": 10, "max": 500,
                       "per_unit": 100, "units_min": 0, "units_max": 5, "unit": "MW",
                       "systemwide_min": 0, "systemwide_max": 700,
                       "decommission": { "mode": "impossible"|"continuous"|"only_complete", "cost": 12 } },
        "operation": { "max_pu": 0.9 | "ts-id", "min_pu": 0.3, "equals_pu": 0.4 | "ts-id",
                       "ramp_up": 0.6, "ramp_down": 0.6,
                       "committable": true, "start_up_cost": 5000, "shut_down_cost": 0,
                       "min_uptime": 3, "min_downtime": 2, "max_startups": 50, "standby_power": 0.02 },
        "storage":   { "energy_capacity": {…}, "max_hours": 4, "charge_eff": 0.95, "discharge_eff": 0.95,
                       "max_charge_rate": 0.25, "max_discharge_rate": 0.25,     // instead of max_hours
                       "self_discharge": 0.001, "depth_of_discharge": 0.1, "inflow": "ts-id",
                       "spill_cost": 5, "initial_soc": 0.5, "cyclic": true,
                       "no_simultaneous_charge_discharge": true },
        "demand_profile": 100 | "ts-id", "demand_curtailable": false,
        "area":   { "max": 5000, "per_capacity": 8 },
        "source": { "max": 0.25 | "ts-id", "unit": "per_area" | "per_cap" | "absolute", "cap": 900 },
        "costs": { "monetary": {                  // only the "monetary" class is priced
            "investment_per_capacity": 6e5, "investment_per_energy_capacity": 1.5e5,
            "investment_per_capacity_distance": 900,                    // transmission only
            "fixed_om": 2000, "fixed_om_fraction": 0.04,                // mutually exclusive
            "variable_om": 45 | "ts-id", "fuel_cost": 20, "purchase": 1000 } },
        "lifetime": 25, "interest_rate": 0.05, "cost_basis": "overnight" | "annualized",
        "emission_factor": 0.2, "annual_output_min": 0, "annual_output_max": 5e4,
        "build_year": 2024, "active": true,
        "node_overrides": { "<node>": { /* any subset of the fields above */ } },
        "native": { "<target>": { … } }
      }
    },
    "transmission": { "<id>": { "carrier": "…", "from": "n1", "to": "n2", "bidirectional": true,
                                "capacity": {…}, "efficiency": 0.97, "distance": 50,
                                "loss_per_distance": 1e-4, "min_flow": 0.05, "emission_factor": 0.001,
                                "energy_consumption": { "carrier": "…", "per_flow": 0.01, "per_flow_distance": 1e-4 },
                                "costs": {…}, "active": true, "native": { … } } },
    "trade": { "<id>": { "node": "n1", "carrier": "…",
                         "import": { "limit": 500 | "ts-id", "price": 60 | "ts-id", "emission_factor": 0.35 },
                         "export": { "limit": 200 | "ts-id", "price": 30 | "ts-id", "emission_factor": 0.3 },
                         "native": { … } } },
    "emission_limits": [ { "name": "co2_cap", "carrier": "co2", "sense": "<=", "limit": 1e6, "period": "…" } ],
    "constraints": [ {                            // portable linear IR: Σ coeff·var[scope] sense bound
      "name": "…", "sense": "<=" , "bound": 800, "period": "…",
      "terms": [ { "coefficient": 1,
                   "variable": "capacity" | "flow_out" | "flow_in" | "storage_cap" | "emissions",
                   "techs": ["…"], "carriers": ["…"], "nodes": ["…"] } ] } ],
    "native": { "<target>": { … } }               // system-wide target-specific config
  },
  "experiment": {
    "mode": "plan" | "operate" | "alternatives" | "pareto" | "monte_carlo" | "stochastic",
    "objective": "min_cost" | "min_emissions",
    "emission_accounting": "net" | "positive_only",           // A: emissions_net vs emissions_pos
    "foresight": "perfect" | "myopic",
    "time_aggregation": { "method": "typical_days" | "resample" | "cluster", "periods": 7,
                          "resolution": "3H", "cluster_series": "ts-id",
                          "keep_full_resolution_for": ["STOR", …] },
    "operate":      { "window": "12h", "horizon": "24h" },    // typed mode options; the legacy
    "alternatives": { "number": 5, "slack": 0.1,              // solver.options namespaces still
                      "scoring_algorithm": "integer" },       // work but warn as deprecated
    "pareto":       { "points": 5 },
    "monte_carlo":  { "samples": 25, "standard_deviation": 0.3, "on": ["technology_capex", …] },
    "allow_unmet_demand": { "enabled": true, "penalty_price": 3000 },
    "copperplate": false,
    "reporting":   { "case_name": "…", "save_logs": "…", "shadow_prices": ["…"] },
    "scenarios": [ { "name": "…", "weight": 0.5, "overrides": { "<dotted.path>": 90 } } ],
    "sweep":     [ { "parameter": "technologies.pv.capacity.max", "values": [200, 400] } ],
    "solver":    { "name": "highs" | "cbc" | "glpk" | "gurobi",
                   "time_limit": 3600, "mip_gap": 0.01, "threads": 8, "options": { … } },
    "custom_math": true,                          // Calliope: render constraint IR into extra math
    "native": { "<target>": { … } }
  }
}
```

**Value union.** Any field typed *Value* accepts a number (scalar), a string
(time-series id from `model.timeseries`, optionally `"ts:"`-prefixed), an
inline per-timestep array `[0.1, 0.4, …]` (interned into `model.timeseries`
under a reserved `_inline:` id before validation), or an indexed object
`{ "data": […], "index": […], "dims": […] }` (Calliope-style; multi-valued
indexed parameters are Calliope-only).

**Dotted parameter paths** (sweep axes and stochastic overrides):
`technologies.<id>.capacity.{max,min,existing}`, `technologies.<id>.efficiency`,
`technologies.<id>.demand_profile` (stochastic), 
`technologies.<id>.costs.monetary.{investment_per_capacity,variable_om}` (sweep) /
`costs.monetary.variable_om` (stochastic), `carriers.<id>.co2_intensity`.

---

## 2. Native passthrough per framework

Every `native` block is scoped to one target (`"native": {"pypsa": …}` is
invisible to the other two; a warning lists blocks the selected target ignores).
Because selection is by key, blocks for all three targets can ride in one
payload side by side — [`examples/payload_full.json`](../examples/payload_full.json)
demonstrates this at every scope.
Native values are merged **last** — verbatim, unvalidated — so they can extend
*or override* emitted attributes. A string naming a time series is **not**
resolved; supply real literals or full data-table definitions.

### PyPSA (`native.pypsa`)

| Attach point | Lands in |
|---|---|
| `carriers.<id>.native` | extra columns on the `carriers.csv` row |
| `nodes.<id>.native` | extra columns on every `buses.csv` row of that node |
| `technologies.<id>.native` | extra columns on the component's CSV row (`generators`/`loads`/`links`/`storage_units`) |
| `transmission.<id>.native` | extra columns on the link row (e.g. `{"x":0.1,"r":0.01}`) |
| `model.native` | `_native.pypsa.json` sidecar (not consumed by run.py — documentation payload) |

Typical native-only uses: electrical parameters (`x`/`r`, Lines/Transformers),
`marginal_cost_quadratic`, `ramp_limit_start_up`, `stand_by_cost`, Stores with
independent charger/discharger links.

### Calliope (`native.calliope`)

| Attach point | Lands in |
|---|---|
| `technologies.<id>.native` | keys merged into the tech's `model.yaml` entry |
| `nodes.<id>.native` | keys merged into the node's entry |
| `carriers.<id>.native` / `model.native` | merged into the respective YAML scopes |

Typical native-only uses: `source_use_min` (resource floor),
`flow_out_parasitic_eff`, `sink_use_min`, `area_use_per_flow_cap`, custom math
files. Scalars broadcast over missing dims
(`config.init.broadcast_input_data: true` is always emitted).

### AdOpT-NET0 (`native.adopt-net0`)

| Attach point | Lands in |
|---|---|
| `technologies.<id>.native` | patched onto the copied database-tech JSON (via the node-keyed `_meme_overrides.json`) |
| `nodes.<id>.native` | deep-merged into the node's `Technologies.json` — **the** way to add unmapped techs (`{"new": ["GasTurbine_simple"]}`) |
| `model.native` | merged into `ConfigModel.json` (solver/MILP gap/scaling options) |

adopt_net0 is database-driven: only physics techs (`pv` → `Photovoltaic`,
`wind` → `WindTurbine_Onshore_1500`, `heat_pump` → `HeatPump_AirSourced`) and
storage (`Storage_Battery`) map canonically. Any other non-demand tech is
**rejected** unless introduced through node-level native.

---

## 3. Support tables

### 3.1 Experiment (run spec)

| Property | P | C | A | Notes |
|---|:-:|:-:|:-:|---|
| `mode: plan` | ✓ | ✓ | ✓ | default |
| `mode: operate` | ✓ rolling horizon (`experiment.operate.horizon`; fixed capacities enforced; no horizon-wide constraints) | ✓ receding horizon (`experiment.operate.{window,horizon}`; capacities become inputs) | ✗ | typed block wins over legacy `solver.options.operate` (deprecation warning) |
| `mode: alternatives` | ✓ MGA (`experiment.alternatives.slack`; baseline + alternative exported) | ✓ SPORES (`experiment.alternatives.{number,slack,scoring_algorithm}`) | ✗ | legacy `options.spores`/`options.mga` warn |
| `mode: pareto` | ✗ | ✗ | ✓ (`experiment.pareto.points`) | |
| `mode: monte_carlo` | ✗ | ✗ | ✓ (`experiment.monte_carlo.{samples,standard_deviation,on}`) | |
| `mode: stochastic` | ✓ two-stage `set_scenarios` | ✗ | ✗ | overrides must be translatable and numeric (validated) |
| `objective: min_cost` | ✓ | ✓ | ✓ | default |
| `objective: min_emissions` | ✗ | ✗ | ✓ | |
| `emission_accounting: positive_only` | ✗ | ✗ | ✓ `emissions_pos` | default `net` everywhere |
| `foresight` | · | · | · | accepted, not yet mapped |
| `time_aggregation: resample` | ✗ | ✓ `config.init.resample` | ✗ | |
| `time_aggregation: typical_days` | ✗ | ✗ | ✓ k-means `typicaldays.N` (+ `keep_full_resolution_for` → `technologies_with_full_res`) | |
| `time_aggregation: cluster` | ✗ | ✗ | ✗ | accepted by the model; no target claims it yet |
| `allow_unmet_demand` | ✗ | ✓ `ensure_feasibility` (⚠ penalty approximated by bigM) | ✓ `energybalance.violation` (needs `penalty_price`) | |
| `copperplate` | ✗ | ✗ | ✓ `energybalance.copperplate` | |
| `reporting` | ⚠ `output_dir` service-managed | ✓ `save_logs`, `shadow_prices` | ✓ `case_name` | |
| `scenarios` | ✓ (stochastic mode) | ✗ | ✗ | |
| `sweep` | ✓ | ✓ | ✓ | orchestrator-level: N independent runs, any target |
| `solver.name` | ✓ highs (default) / cbc / glpk / gurobi | ✓ cbc (default; `highs` → cbc with warning) | ✓ glpk (fixed) | |
| `solver.time_limit/mip_gap/threads` | ✓ mapped per solver (highs/gurobi/cbc; else ⚠) | ✓ mapped (gurobi/cbc; else ⚠) | ✓ `mipgap`/`timelim` (s→h)/`threads` | |
| `solver.options` | ✓ legacy mode options (deprecated) | ✓ legacy mode options (deprecated) | ✓ legacy mode options (deprecated) | |
| `custom_math` | — | ✓ render toggle for constraint IR | — | ratio/piecewise math renders regardless |
| `experiment.native` | · | · | · | reserved |

### 3.2 Model-level inputs

| Property | P | C | A | Notes |
|---|:-:|:-:|:-:|---|
| `metadata.*` | · | · | · | informational; `name` is required |
| `time.start/end/resolution` | ✓ snapshots | ✓ time index | ✓ Topology dates | required + ISO-parseable; PyPSA snapshot count follows the longest series |
| `time.subset` | ✗ dropped | ✓ `init.subset.timesteps` | ✗ dropped | P/A: use start/end instead |
| `time.timesteps` (explicit labels) | ✓ snapshot labels | ✓ data-table index | ✗ rejected (needs a regular date_range) | |
| `time.weights` | ✓ `snapshot_weightings` (scalar or series) | ✓ `timestep_weights` (scalar; series ✗) | ✗ rejected | |
| `periods` | · | · | · | reserved (single investment period); rich `{name,year,…}` entries accepted |
| `discount_rate` (global) | ✓ overrides annuity rate | ✓ overrides `cost_interest_rate` | ✓ `global_discountrate` | |
| `carriers.<id>.co2_intensity` | ✓ `carriers.co2_emissions` | ✓ co2 cost class on fuel inflow | ✓ `emission_factor` | |
| `carriers.<id>.unit/color` | · | · | · | |
| `timeseries` (inline) | ✓ | ✓ | ✓ | file-sourced series: referenced, not copied |
| `transmission` | ✓ Link | ✓ transmission tech | ✗ rejected (Networks unmapped) | |
| `trade` | ✓ market generators | ✓ market supply/demand techs | ✓ carrier CSV columns | |
| `emission_limits` | ✓ GlobalConstraint `primary_energy` | ✓ custom-math cap on `cost[costs=co2]` | ✓ `costs_emissionlimit` objective | |
| `constraints` (unscoped, single-variable) | ✓ GlobalConstraint | ✓ custom math | ✗ rejected | |
| `constraints` (tech/node-scoped or multi-term) | ✓ run.py linopy constraints (scope-exact; no scoped `emissions` terms) | ✓ custom math | ✗ rejected | |
| `model.native` | ✓ sidecar | ✓ YAML merge | ✓ ConfigModel merge | |

### 3.3 Node properties

| Property | P | C | A | Notes |
|---|:-:|:-:|:-:|---|
| `coords` | ✓ bus `x`/`y` | ✓ `latitude`/`longitude` | ✓ `NodeLocations.csv` | |
| `available_area` | ✗ (area is Calliope-only) | ✓ `available_area` | ✗ | gated with `technology.area` |
| `climate` (ghi, dni, dhi, temp_air, rh, ws10, hydro_inflow) | — | — | ✓ `ClimateData.csv` (scalar or series) | feeds physics techs |
| `allowed_techs` | · | · | · | informational (tech placement comes from `technology.node`) |
| `native` | ✓ bus columns | ✓ node YAML | ✓ `Technologies.json` merge | |

### 3.4 Technology properties

| Property | P | C | A | Notes |
|---|:-:|:-:|:-:|---|
| `role: supply` | ✓ Generator | ✓ `base_tech: supply` | ✓ physics DB tech, else ✗ | A: needs `AdoptDBTech` mapping or node-native |
| `role: demand` | ✓ Load | ✓ demand tech + data table | ✓ `Demand` column | scalar demand expands to a series everywhere it must |
| `role: conversion` | ✓ Link | ✓ conversion tech | ✗ (no DB mapping) | |
| `role: storage` | ✓ StorageUnit | ✓ storage tech | ✓ `Storage_Battery` | |
| `carrier_in` / `carrier_out` | ✓ buses per (node, carrier) | ✓ | ✓ | |
| `efficiency` (scalar) | ✓ `efficiency` | ✓ `flow_out_eff` | ✓ via DB tech performance | series → ✗ everywhere |
| `flows` (multi-port, rigid ratios) | ✓ multi-output Link (`bus2`/`efficiency2`, negative eff for extra inputs) | ✓ carrier lists + ratio-pinning math (fuel single-counted) | ✗ | ratios are rigid on both: demands must be consistent |
| `performance: constant` | ✓ | ✓ | ✓ | |
| `performance: piecewise` (+`min_load`) | ✗ | ✓ `piecewise_constraints` math — conversion role, **fixed capacity**, no flows | ✗ | C: `flow_cap` also caps inflow — keep curve x ≤ capacity |
| `performance: physics` (pv, wind, heat_pump) | ✓ with `precomputed` profile | ✓ with `precomputed` → `source_use_max` table | ✓ native (computes from climate) | `open_hydro`: A has no mapping → ✗ |
| `capacity.existing` | ✓ `p_nom` | ✓ `flow_cap_min = flow_cap_max` (fixed) | ✓ `existing` size | |
| `capacity.expandable` | ✓ `p_nom_extendable` | ✓ bounds only | ✓ `new` tech | |
| `capacity.min` / `max` | ✓ `p_nom_min`/`p_nom_max` | ✓ `flow_cap_min`/`flow_cap_max` | ✓ `size_min`/`size_max` overrides | |
| `capacity.per_unit` + `units_min/max` | ✓ implied `p_nom_min`/`_max` = units·per_unit | ✓ `flow_cap_per_unit` + `purchased_units_min/max` (committable); implied bounds otherwise | ✓ implied `size_min`/`size_max` | explicit `min`/`max` win |
| `capacity.systemwide_min/max` | ✓ synthesized sidecar constraint (✗ in operate/stochastic) | ✓ `flow_cap_min/max_systemwide` | ✗ | |
| `capacity.decommission` | impossible only (else ✗) | ✓ impossible/continuous (`only_complete` ✗) | ✓ full enum + `decommission_cost` | default: impossible |
| `operation.max_pu` (scalar) | ✓ `p_max_pu` | ✓ `source_use_max` + `per_cap` (supply only) | ✗ | |
| `operation.max_pu` (series) | ✓ `generators-p_max_pu.csv` | ✓ data table (supply only) | ✗ | |
| `operation.min_pu` (scalar) | ✓ `p_min_pu` | ✓ `flow_out_min_relative` | ✗ | series → ✗ everywhere |
| `operation.equals_pu` (scalar / series) | ✓ pins `p_min_pu` = `p_max_pu` (+ both CSVs) | ✓ `source_use_equals` (supply) | ✗ | fixed, non-curtailable dispatch |
| `operation.ramp_up` / `ramp_down` | ✓ `ramp_limit_up`/`_down` | ✓ `flow_ramping` (symmetric, first non-nil) | ✗ | series → ✗ everywhere |
| `operation.committable` | ✓ MILP UC | ✓ MILP: `cap_method: integer` + `integer_dispatch` + `purchased_units_max` (needs unit size) | ✗ | |
| `operation.start_up_cost` / `shut_down_cost` | ✓ | ✗ (no 0.7 math) | ✗ | |
| `operation.min_uptime` / `min_downtime` | ✓ `min_up_time`/`min_down_time` (snapshot counts) | ✗ | ✗ | |
| `operation.max_startups` / `standby_power` | ✗ | ✗ | ✓ `Performance.max_startups`/`standby_power` | |
| `storage.energy_capacity` | (via `max_hours`) | ✓ `storage_cap_max`/`_min` | (fixed E/P ratio of DB tech) | |
| `storage.max_hours` | ✓ `max_hours` | ✓ `storage_cap_max = max_hours·cap_max` | — | mutually exclusive with the rate bounds |
| `storage.max_charge_rate` / `max_discharge_rate` | ✗ (use `max_hours` or native Stores) | ✓ `flow_cap_per_storage_cap_max` (rates must be equal) | ✓ `Flexibility.charge_rate`/`discharge_rate` | independent power/energy sizing |
| `storage.charge_eff` / `discharge_eff` | ✓ `efficiency_store`/`_dispatch` | ✓ `flow_in_eff`/`flow_out_eff` | ✓ DB performance | |
| `storage.self_discharge` | ✓ `standing_loss` | ✓ `storage_loss` | — | |
| `storage.depth_of_discharge` | ✗ (no min-SOC on StorageUnit) | ✓ `storage_discharge_depth` | ✗ | |
| `storage.no_simultaneous_charge_discharge` | ✗ | ✓ `force_async_flow` | ✓ `allow_only_one_direction` | |
| `storage.inflow` | ✓ scalar or `storage_units-inflow.csv` | ✗ | ✗ | |
| `storage.spill_cost` | ✓ | ✗ | ✗ | |
| `storage.initial_soc` | ✓ `state_of_charge_initial` | ✓ `storage_initial` | — | |
| `storage.cyclic` | ✓ `cyclic_state_of_charge` | ✓ `cyclic_storage` (operate: forced false) | — | |
| `demand_profile` (scalar / series) | ✓ `p_set` / `loads-p_set.csv` | ✓ data table (scalar expanded) | ✓ `Demand` column | |
| `demand_curtailable` | ✗ | ✓ `sink_use_max` instead of `sink_use_equals` | ✗ | |
| `area.max` | ✗ gated | ✓ `area_use_max` (+node `available_area`) | ✗ gated | |
| `area.per_capacity` | — | ⚠ not emitted (use native `area_use_per_flow_cap`) | — | |
| `source.max` (scalar / series) | ✗ gated | ✓ `source_use_max` (+ data table) | ✗ gated | |
| `source.unit` / `source.cap` | — | ✓ `source_unit` / `source_cap_max` | — | |
| `costs.monetary.investment_per_capacity` | ✓ annualized → `capital_cost` | ✓ `cost_flow_cap` + `cost_interest_rate` | ✓ `unit_CAPEX` | overnight basis needs `lifetime`+`interest_rate` |
| `costs.monetary.investment_per_energy_capacity` | ⚠ dropped (no per-MWh slot on StorageUnit) | ✓ `cost_storage_cap` | ⚠ dropped | |
| `costs.monetary.fixed_om` | ✓ folded into `capital_cost` | ✓ `cost_om_annual` | ✓ `OPEX_fixed` | mutually exclusive with `fixed_om_fraction` |
| `costs.monetary.fixed_om_fraction` | ✓ fraction·investment folded into `capital_cost` | ✓ `cost_om_annual_investment_fraction` | ✓ `OPEX_fixed` (adopt's native convention) | needs overnight basis |
| `costs.monetary.variable_om` (scalar) | ✓ `marginal_cost` | ✓ `cost_flow_out` (merged multi-class) | ✓ `OPEX_variable` | |
| `costs.monetary.variable_om` (series) | ✓ `*-marginal_cost.csv` | ✗ | ✗ | |
| `costs.monetary.fuel_cost` (scalar) | ✓ fuel/efficiency folded into `marginal_cost` | ✓ `cost_flow_in` (merged with co2 class) | ✗ (price the input via trade) | series → ✗ |
| `costs.monetary.purchase` | ✗ (not emitted) | ✓ `cost_purchase` (committable) | ✗ | |
| `costs.<other class>` | ⚠ warned & dropped | ⚠ (except co2 accounting) | ⚠ | only `monetary` is priced |
| `lifetime` / `interest_rate` / `cost_basis` | ✓ CRF annualization | ✓ `lifetime` + `cost_interest_rate` | ✓ economics | global `discount_rate` overrides the rate |
| `emission_factor` | ✗ (carrier-wide accounting; set carrier intensity) | ✓ co2 cost class on `cost_flow_out` | ✓ `Performance.emission_factor` | |
| `annual_output_min` / `max` | ✓ `e_sum_min`/`e_sum_max` (supply only) | ✗ | ✗ | |
| `build_year` | ✓ `build_year` | ⚠ informational | ⚠ informational | |
| `active: false` | ✓ `active` column | ✓ `active: false` | ✓ omitted from `Technologies.json` | |
| `node_overrides` | ✓ one component per node | ✓ `nodes.<n>.techs` overrides (capacity + costs projected) | ✓ per-node `Technologies.json` + node-keyed overrides | |
| `native` | ✓ CSV columns | ✓ YAML merge | ✓ tech-JSON patch | merged last, verbatim |

### 3.5 Transmission & trade properties

| Property | P | C | A | Notes |
|---|:-:|:-:|:-:|---|
| `transmission.capacity` | ✓ `p_nom*` | ✓ `flow_cap_max` | ✗ | systemwide bounds as for technologies |
| `transmission.efficiency` | ✓ (scalar; series ✗) | ✓ `flow_out_eff` | ✗ | |
| `transmission.bidirectional` | ✓ `p_min_pu: -1` | ✓ `one_way: true` when false | ✗ | |
| `transmission.distance` | ⚠ not emitted (feeds loss/aux folding) | ✓ `distance` | ✗ | |
| `transmission.loss_per_distance` | ✓ folded into `efficiency` (needs `distance`) | ✓ `flow_out_eff_per_distance` | ✗ | |
| `transmission.min_flow` | ✓ `p_min_pu` (one-way arcs only) | ✓ `flow_out_min_relative` | ✗ | |
| `transmission.emission_factor` | ✗ (carrier-wide accounting) | ✓ co2 cost class on the link flow | ✗ | |
| `transmission.energy_consumption` | ✓ multi-port link (`bus2`, negative `efficiency2`; one-way arcs only) | ✗ | ✗ | |
| `transmission.active: false` | ✓ `active` column | ✓ `active: false` | ✗ | |
| `transmission.costs.investment_per_capacity_distance` | ⚠ warned & dropped | ✓ `cost_flow_cap_per_distance` | ✗ | |
| `transmission.costs` (other fields) | ⚠ warned & dropped | ⚠ warned & dropped | ✗ | no lifetime/interest on transmission to annualize |
| `trade.import.limit` (scalar / series) | ✓ `p_nom_max` / ✓ fixed `p_nom=1` + `p_max_pu` series | ✓ `flow_cap_max` / ✗ | ✓ / ✓ per-timestep column | |
| `trade.import.price` (scalar / series) | ✓ / ✓ `generators-marginal_cost.csv` | ✓ / ✗ | ✓ / ✓ per-timestep column | |
| `trade.import.emission_factor` | ⚠ not emitted (set carrier intensity) | ✓ co2 cost class | ✓ `Import emission factor` | |
| `trade.export.limit` (scalar / series) | ✓ `p_nom_max` / ✓ fixed `p_nom=1` + negated `p_min_pu` series | ✓ `sink_use_max` / ✗ | ✓ / ✓ | |
| `trade.export.price` (scalar / series) | ✓ (+mc, revenue via p<0) / ✓ | ✓ (−`cost_flow_in`) / ✗ | ✓ / ✓ | |
| `trade.export.emission_factor` | ⚠ not emitted | ✓ co2 cost class | ✓ `Export emission factor` | |
| `trade.native` | ✓ columns on the market generators | ✓ merged into the market techs | ⚠ no merge anchor (warned) | |

### 3.6 Outputs

| Output | P | C | A |
|---|---|---|---|
| Native inputs on disk | `run_i/input/*.csv` + sidecars | `run_i/input/model.yaml` + data tables + `additional_math.yaml` | `run_i/input/input_data/` tree |
| Executed driver | `run_i/run.py` | Calliope CLI (command in `results.json`) | `run_i/run.py` |
| Solved model | `run_i/output/network.nc` (+ `network_baseline.nc` for MGA) | `run_i/output/results.nc` + `output/csv/*` (per-variable CSVs incl. `flow_out`, `storage`, `purchased_units`, costs) | `run_i/input/results/` (HDF5 per solve + `Summary.xlsx` + `solver_log.txt`) |
| Objective / status in log | `status ok optimal`, `objective <v>` (non-optimal ⇒ non-zero exit ⇒ job `failed`) | `Optimal - objective value <v>`, `Calliope run complete` | Pyomo block (`Termination condition: optimal`, bounds) |
| Job artifacts (all targets) | `GET /jobs/{id}` zip: `metadata.json`, `log.txt`, `results.json` (target-labeled per-run exit/stdout/plan), `config.json` (byte-identical initial config), `files/` (everything above; multi-target jobs nest as `files/<target>/run_i/…`) | — | — |
| Status endpoint | `GET /jobs/{id}/status`: `state` (queued/running/succeeded/failed), live `log`, per-run results, timings | — | — |
| Multi-target submit | `?target=pypsa,calliope,adopt-net0` or `target=all` on validate/convert/simulate: per-target verdicts/entrypoints, one job running every framework (422 up front if ANY requested target rejects the payload; see [`examples/shared_full.json`](../examples/shared_full.json)) | — | — |

---

## 4. Cross-target guarantees

- A job only reaches the runner if **every** property above resolves to ✓ or ⚠
  for the selected target (⚠ always named in `warnings`). For a multi-target
  request (`?target=all`) this holds per requested target — one rejection
  fails the whole submit up front.
- The **portable maximum** is a real, runnable artifact:
  [`examples/payload_full.json`](../examples/payload_full.json) uses the largest
  property set all three frameworks accept simultaneously (with each
  framework's extras in its own `native` namespace) and solves to optimal on
  all of them from a single `?target=all` submit.
- The identical dispatch model produces the **same objective on all three
  frameworks** (`TestE2ECrossTargetConsistency`, ±1%).
- Multi-output conversion ratios are **rigid on PyPSA and Calliope alike**
  (heat/power fixed per unit of fuel); over-determined demand patterns go
  infeasible rather than silently rebalancing, and fuel is single-counted.
- Trade economics are sign-verified: profitable exports are exploited
  (`TestE2ETradeExportRevenue`), time-varying prices steer dispatch
  (`TestE2ETradePriceSeries`), scoped constraints bind
  (`TestE2ESidecarConstraintBinds`).
