# Capability matrix


Property-by-property support (every input, node, tech, transmission/trade and
output property × target, plus each framework's native attach points) is
catalogued in the [capability reference](reference/capability-reference.md); this page summarizes the
feature-level matrix that validation enforces.

Each feature maps to a first-class schema field or a run mode. `ValidateFor`
rejects (422) any payload that uses a feature the selected target doesn't
support. Support is a sparse map — absence means unsupported.

| Feature key | Schema field / concept | PyPSA | Calliope 0.7 | Calliope 0.6 | AdOpT | Enforced |
|---|---|:--:|:--:|:--:|:--:|:--:|
| `mode:plan` | `experiment.mode` | ✓ | ✓ | ✓ | ✓ | yes |
| `mode:operate` | `experiment.mode` (fixed caps; PyPSA = rolling horizon) | ✓ | ✓ | ✓ | | yes |
| `mode:alternatives` | `experiment.mode` (PyPSA = MGA, Calliope = SPORES) | ✓ | ✓ | ✓ | | yes |
| `mode:pareto` | `experiment.mode` | | | | ✓ | yes |
| `mode:monte_carlo` | `experiment.mode` | | | | ✓ | yes |
| `mode:stochastic` | `experiment.mode` + `experiment.scenarios` (two-stage, `n.set_scenarios`) | ✓ | | | | yes |
| `time_aggregation` | `experiment.time_aggregation` (Calliope 0.7: `resample` only; 0.6: `resample` + `typical_days` k-means; AdOpT: `typical_days` only) | | ✓ | ✓ | ✓ | yes (per method) |
| `multi_carrier_conversion` | `technology.flows` (rigid ratios on every target; 0.6 = `conversion_plus` tiers, one input) | ✓ | ✓ | ✓ | | yes |
| `emission_limit` | `model.emission_limits` | ✓ | ✓ | ✓ | ✓ | yes |
| `import_export` | `model.trade` | ✓ | ✓ | ✓ | ✓ | yes |
| `committable` | `operation.committable` (Calliope = MILP integer-unit subset) | ✓ | ✓ | ✓ | | yes |
| `node_override` | `technology.node_overrides` | ✓ | ✓ | ✓ | ✓ | yes |
| `linear_constraint` | `model.constraints` (PyPSA: GlobalConstraint or run.py sidecar; 0.6: single-term `group_constraints`) | ✓ | ✓ | ✓ | | yes |
| `area_constraints` | `technology.area` | | ✓ | ✓ | | yes |
| `source_constraints` | `technology.source` | | ✓ | ✓ | | yes |
| `indexed_params` | multi-valued `Value` | | ✓ | | | yes (multi-valued only) |
| `piecewise_performance` | `performance.type: piecewise` (Calliope: fixed-capacity conversion) | | ✓ | | | yes |
| `physics_performance` | `performance.type: physics` | | | | ✓ | yes (needs precomputed off AdOpT) |
| `power_flow` | `experiment.mode: power_flow` + `node.electrical`, `transmission.electrical`, `technology.dispatch` | ✓ | | | | yes (mode gate; `dispatch` rejected outside the mode) |
| `custom_math` | `experiment.custom_math` → `additional_math.yaml` | | ✓ | | | rendered (linear IR + emission limits) |
| `objective:min_emissions` | `experiment.objective` | | | ✓ | ✓ | yes |

**Enforcement nuances.** Modes route through `modeFeature(mode)`. Every claimed
cell is backed by a runnable scenario (`TestCapabilityCoverage` fails the build
otherwise). PyPSA operate requires fixed capacities and rejects horizon-wide
constraints; PyPSA stochastic validates that every scenario override translates
to a component attribute. Calliope committable is the MILP subset (integer
units + min stable load) — start/stop costs and min up/down times are rejected
with a pointer to `native.calliope`. AdOpT accepts only techs that map onto its
technology database (pv/wind/heat_pump physics, storage); anything else is
rejected with a pointer to node-level `native.adopt-net0`. `power_flow` is
claimed by PyPSA alone and is both a mode and a field family: in mode
`power_flow` the electrical fields become Lines, Transformers and `v_nom`
buses solved by `lpf()`/`pf()`; in every optimization mode a `dispatch` is
rejected (the solver decides dispatch) and electrical parameters are named
as unused while the arcs stay transport Links ([ADR-0008](decisions/0008-power-flow-as-a-pypsa-run-mode.md)).
`physics_performance` off AdOpT requires a `performance.precomputed` series
(else 422). `custom_math` is a Calliope 0.7 render toggle (`experiment.custom_math`,
default on) for the portable `constraints`/`emission_limits` IR; ratio-pinning
and piecewise math render regardless (they are model physics, not user math).
Calliope 0.6 has no custom math: constraints and emission limits become
`group_constraints`, which bound one quantity (capacity, production,
consumption, storage capacity or a cost class) over a tech/node set — so only
single-term constraints are accepted there (the coefficient folds into the
bound); multi-term constraints, multi-input `flows`, `time.weights`, an export
without a limit, indexed parameters and piecewise performance are rejected
with a pointer to the 0.7 target.


## Canonical → framework mapping (selected)

| Canonical | PyPSA | Calliope 0.7 (`calliope`) | Calliope 0.6 (`calliope-0.6`) | AdOpT-NET0 |
|---|---|---|---|---|
| node + carrier | one **Bus** per (node, carrier) | orthogonal `nodes` / carriers | `locations` / carriers | orthogonal nodes / carriers |
| `role: supply` | `Generator` | `base_tech: supply` | `parent: supply` (`supply_plus` when `source.cap` is set) | `Res` / physics tech |
| `role: conversion` + `flows` | multi-output **Link** (`bus2`/`efficiency2`) | `carrier_out` list + ratio-pinning math | `parent: conversion_plus`, `carrier_out_2/_3` tiers, `carrier_ratios` relative to the primary output | multi-carrier conversion |
| `role: storage` | `StorageUnit` | `base_tech: storage` | `parent: storage` (one `energy_eff` for both directions, model-wide `run.cyclic_storage`) | `Storage` |
| `transmission` | `Link` across buses | transmission tech (`link_from`/`link_to`) | transmission tech placed in `links.<from>,<to>` (+ `distance`) | `Networks` + matrices |
| time series | per-attribute CSVs | `data_tables` (`table`, `add_dims.inputs`) + CSVs | `file=<file>.csv:<node>` references into `timeseries_data/` (demand negative) | per-node CSV columns |
| overnight cost + `lifetime`/`interest_rate` | annualized `capital_cost` (CRF) | `cost_flow_cap` + `cost_interest_rate` (annualized internally) | `costs.monetary.energy_cap` + `interest_rate` + `constraints.lifetime` (both required by 0.6; lifetime 1 / rate 0 when absent or already annualized) | economics block |
| carrier `co2_intensity` | carrier `co2_emissions` | per-tech `cost_flow_in[costs=co2]` on consumed fuels + `objective_cost_weights[co2]=0` | `costs.emissions.om_con` on consuming techs + `run.objective_options.cost_class.emissions: 0` | emission factors |
| `emission_limits` | `global_constraints.csv` (primary_energy) | `additional_math.yaml` constraint on `cost[costs=co2]` | `group_constraints.<name>.cost_max.emissions` | emission targets |
| `constraints` (linear IR) | GlobalConstraint or `_constraints.json` | `additional_math.yaml`, registered via `config.init.math_paths`/`extra_math` (`sum(flow_cap[techs=pv]) <= …`, one member per dim; multi-member sets expand into a sum) | single-term → `group_constraints` (`energy_cap_*`, `carrier_prod_*`, `carrier_con_*`, `storage_cap_*`, `cost_*`) | constraints |
| `experiment.mode` | (native) | `config.init.mode`: plan→`base`, operate→`operate`, alternatives→`spores` (+ `config.solve.spores`) | `run.mode`: plan / operate (+ `run.operation` window/horizon hours) / spores (+ `run.spores_options`, `systemwide_cost_max` group, `spores_score` costs) | `ConfigModel.optimization`: pareto→`objective: pareto`, monte_carlo→`monte_carlo.N > 0`; `time_aggregation: typical_days`→`typicaldays.N`. Tunable via `experiment.solver.options` (`monte_carlo.N`, `pareto.points`) |
| `experiment.time_aggregation` | — | `config.init.resample` | `model.time`: `resample`, or `apply_clustering` (k-means, `k` = periods, fixed `random_seed`) | `typicaldays.N` |
| `experiment.objective: min_emissions` | — | — | `objective_options.cost_class: {monetary: 0, emissions: 1}` | `costs_emissionlimit`/emissions objective |
| `experiment.solver` | passed through (e.g. `highs`) | `highs`→`cbc` (HiGHS can't drive Calliope's pyomo backend) | `highs`→`cbc` | forced `glpk` (adopt supports only gurobi/glpk) |
| `technology` (mapped) | `Generator`/`Link`/`StorageUnit` | `techs` entry | `techs` entry (`essentials`/`constraints`/`costs`) | named database tech (`Storage_Battery`, `Photovoltaic`, …) copied + economics-overridden |
| `trade` | market generators (import/export) | supply/demand + `flow_export` | supply (`om_prod` = price) / demand (`resource: -limit`, `force_resource: false`, `om_con` = −price) | `carrier_data` import/export columns |
| `performance: physics` | precomputed → `p_max_pu` + `_performance.json` | precomputed series | precomputed series (`resource`, `energy_per_cap`) | native tech type (pvlib, hydro, COP) |
| `native` | `native.pypsa` | `native.calliope` (flat merge) | `native["calliope-0.6"]` (deep merge into the nested mapping) | `native.adopt-net0` |


## Parameter support: shared vs framework-specific

Design rule: a concept that **more than one framework** supports is a first-class
canonical field emitted for every applicable target; a concept **only one
framework** has is supplied through that framework's `native` block on the tech
or node (`native.pypsa` / `native.calliope` / `native["calliope-0.6"]` /
`native.adopt-net0`), which is merged into the emitted output. A framework
version with its own input dialect has its own key: `native.calliope` is 0.7
syntax and is never applied by the `calliope-0.6` target.

All emitters honor their `native` block at **tech and node level** (PyPSA also
merges it into buses/carriers/links; Calliope 0.6 deep-merges it into the
nested `essentials`/`constraints`/`costs` mapping and applies model- and
experiment-level blocks to the whole document; AdOpT deep-merges it into the
tech economics and node `Technologies.json`, and the model-level block into
`ConfigModel.json`). PyPSA additionally drops a whole-model `native.pypsa` block
into a `_native.pypsa.json` sidecar for the runner to apply.

### Shared (canonical field → per-framework param)

| Canonical field | PyPSA | Calliope 0.7 | AdOpT |
|---|---|---|---|
| `capacity.max` / `.min` | `p_nom_max` / (bound) | `flow_cap_max` / `flow_cap_min` | capacity bounds |
| `efficiency` / `performance` | `efficiency` | `flow_out_eff` | efficiency |
| `costs.investment_per_capacity` | `capital_cost` (CRF) | `cost_flow_cap` (+`cost_interest_rate`) | capex |
| `costs.variable_om` | `marginal_cost` | `cost_flow_out` | variable O&M |
| `costs.fixed_om` | folded into `capital_cost` | `cost_om_annual` | fixed O&M |
| `costs.investment_per_energy_capacity` | (via `max_hours`) | `cost_storage_cap` | energy capex |
| `costs.purchase` | — | `cost_purchase` | purchase cost |
| `lifetime` / `interest_rate` | annualization inputs | `lifetime` / `cost_interest_rate` | economics |
| `operation.min_pu` | `p_min_pu` | `flow_out_min_relative` | min load |
| `operation.ramp_up` / `ramp_down` | `ramp_limit_up` / `_down` | `flow_ramping` | ramping |
| `storage.charge_eff` / `discharge_eff` | `efficiency_store` / `_dispatch` | `flow_in_eff` / `flow_out_eff` | storage eff |
| `storage.self_discharge` | `standing_loss` | `storage_loss` | self-discharge |
| `storage.initial_soc` | `state_of_charge_initial` | `storage_initial` | initial SOC |
| `storage.cyclic` | `cyclic_state_of_charge` | `cyclic_storage` | cyclic |
| `storage.energy_capacity` / `max_hours` | `max_hours` | `storage_cap_max` / `_min` | energy capacity |
| carrier `co2_intensity` + `emission_limits` | carrier `co2_emissions` + global constraint | `cost_flow_in[costs=co2]` + `objective_cost_weights[co2]=0` | emission factor/target |
| `constraints` (linear IR) | GlobalConstraint (unscoped) / run.py linopy constraints (tech- or node-scoped) | `additional_math.yaml` | constraints |

### Shared concept, partially mapped

| Concept | PyPSA | Calliope | AdOpT |
|---|---|---|---|
| Unit commitment | `committable`, `start_up_cost`, `min_up_time`, … | MILP subset: integer units + `flow_out_min_relative` (start/stop cost + up/down times **rejected**, use `native.calliope`) | rejected (map a CONV database tech via node `native.adopt-net0`) |
| Availability / capacity factor (`operation.max_pu`, `performance.precomputed`) | `p_max_pu` (materialized series) | `source_use_max` data table + `source_unit: per_cap` | RES physics techs compute from `node.climate` |
| Hydro `inflow` / `spill_cost` | `inflow` CSV / `spill_cost` | rejected (use `native.calliope`) | rejected |
| Series prices (`trade.*.price`) | `generators-marginal_cost.csv` | rejected (scalar only) | per-timestep price columns |

### Framework-specific (only that framework; use its `native` block)

| Framework | Params | Supply via |
|---|---|---|
| PyPSA | power flow (`x`, `r`, `s_nom`, …) — Lines are not emitted first-class | `native.pypsa` |
| Calliope | `flow_out_parasitic_eff`, `*_eff_per_distance`, `storage_discharge_depth`, `storage_cap_per_unit`, `flow_cap_*_systemwide`, `sink_use_min`, `area_use_per_flow_cap`, `source_use_equals`/`source_use_min` (must-run) | `native.calliope` |
| AdOpT | unmapped techs (generic supply/conversion, `GasTurbine`, `CHP`, …), per-tech physics params, climate data | `native.adopt-net0` (name a real database tech + override) |

`native` blocks are merged **last**, so they can also override an emitted
attribute. First-class Calliope-only fields in the schema are emitted directly:
`technology.area.max` → `area_use_max`, `technology.source` →
`source_use_max`/`source_cap_max`/`source_unit`, `experiment.custom_math`;
everything else Calliope-specific rides in `native.calliope`.

**Calliope source model / forcing resource consumption.** Calliope 0.7 has no
`force_resource` flag (that was 0.6). Resource use is governed by which parameter
is set: `source_use_max` allows curtailment; **`source_use_equals` forces exact
consumption** (must-run, no curtailment); `source_use_min` is a lower bound. The
canonical `technology.source` field maps to the curtailable bound:

```jsonc
"technologies": { "pv": {
  "role": "supply", "node": "n1", "carrier_out": "electricity",
  "source": {
    "max": 0.25,          // -> source_use_max (curtailable resource bound)
    "unit": "per_area"    // -> source_unit: per_area | per_cap | absolute
  }
}}
```

Native values are merged **verbatim**: a string naming a time series (e.g.
`"source_use_equals": "pv_resource"`) is NOT resolved against
`model.timeseries` — Calliope would try to parse it as a number and abort. Use
scalars (broadcast over timesteps via `config.init.broadcast_input_data`), or
supply a full `data_tables` entry plus the CSV through the native block. Also
beware `source_use_equals` pins consumption per timestep: combined with a
time-varying demand and no storage/export it renders the model infeasible —
prefer `source_use_max` unless must-run behavior is intended.


