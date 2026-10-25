# Coverage roadmap

This page plans the work that closes the gap between what the three
frameworks accept natively and what the canonical model can express. It is
the follow-up to a measurement taken on 2026-10-25 (see
[§7](#7-reproducing-the-measurement)): every scenario and example payload was
emitted for every target in dry-run mode, and the union of what the emitters
wrote was compared with the native input space of the pinned framework
releases.

Two rules shape the plan:

1. **Additive first.** A payload that validates today keeps validating and
   produces the same native input tomorrow, unless the changelog names the
   change. New capability is delivered as optional fields, new feature keys,
   new emitter paths and new scenarios. Phases 0 and 1 are entirely additive
   and ship in `0.2.x` releases.
2. **Breaking changes travel together.** Anything that removes a field,
   changes a default or a representation, or alters what a target does with
   an existing payload is collected in [§4](#4-phase-2-breaking-changes-for-030)
   and ships as one `0.3.0` release. Each item is announced by a deprecation
   warning in the preceding `0.2.x` release, so consumers see it in
   `warnings` before it bites. Under SemVer a `0.x` minor may break; once MEME
   reaches `1.0`, this bundle discipline moves to major releases.

The design rules of [ADR-0002](decisions/0002-capability-gated-validation.md)
(nothing is dropped silently), [ADR-0003](decisions/0003-native-passthrough.md)
(single-framework concepts ride in `native`) and
[ADR-0004](decisions/0004-golden-files-freeze-the-emitter-contract.md) (golden
files freeze the emitter contract) apply throughout. A concept becomes a
canonical field only when at least two frameworks have it; everything else
stays reachable through `native` and is listed here only to show that it is
reachable.

## 1. Baseline

The numbers the plan starts from. "Written" means at least one scenario or
example payload makes the emitter produce the parameter; "code path" means
the emitter can write it but no payload in the corpus does.

| Framework | Denominator | Written | Code path, no coverage | Neither |
|---|---|---|---|---|
| PyPSA 1.2.4 | 237 input attributes on the 11 core components | 109 (46 %) | 35 | 93 (+ 70 on Process, Shape, SubNetwork and the standard-type tables) |
| Calliope 0.7.0 | 64 math parameters (+ 23 lookups, 48 config fields) | 38 (59 %) | 8 | 18 |
| Calliope 0.6.8 | 156 parameters in `defaults.yaml` | 67 (43 %) | 17 | 72 |
| AdOpT-NET0 0.1.10 | 54 database technologies, 14 networks, 41 `ConfigModel` keys | 4 technologies, 0 networks, 16 keys | 1 technology (`HeatPump_AirSourced`) | 49 technologies, 14 networks, 25 keys |

Seen from the canonical side, 42 of the 137 properties in the
[capability reference](reference/capability-reference.md) are honored by all
three frameworks, 74 are accepted everywhere, and the 26-key feature matrix
has four keys every default target claims.

Two findings are defects rather than gaps, because the capability reference
claims them:

- PyPSA `StorageUnit` rows never receive `p_nom_max`, `p_min_pu` or
  `p_max_pu`: `capacity.max` and `operation.min_pu`/`max_pu` on a
  `max_hours` storage are dropped without a warning
  (`internal/target/pypsa/emitter.go`, the `RoleStorage` branch skips
  `applyOperation`, and the row has no `p_nom_max`).
- AdOpT `Storage_Battery` overrides never carry `eta_in`/`eta_out`:
  `storage.charge_eff`/`discharge_eff` are accepted and ignored, while the
  reference lists them as emitted DB performance.

## 2. Phase 0: code and corpus agree (0.2.x, no schema change)

Goal: every emitter code path is exercised by a scenario, every claim in the
capability reference is backed by an emitted attribute, and the build fails
when that stops being true.

### 2.1 Attribute-level coverage guard

`TestCapabilityCoverage` works at feature level. Add an attribute-level
companion: emit every scenario and example for every target in a test, union
the written attribute names per native component (PyPSA CSV columns and
`<component>-<attr>.csv` files, Calliope YAML keys and data-table
parameters, AdOpT `ConfigModel`/override keys and `Technologies.json` names),
and freeze the union as `internal/target/testdata/golden/attribute_matrix.json`.
A code path that loses its scenario fails the build with the attribute's
name, the way a golden tree does for bytes. This extends ADR-0004 and makes
the baseline above a maintained number instead of a one-off.

### 2.2 Scenarios for existing code paths

Each item below is emitted by the current code and covered by no payload.
One scenario per line, goldens included.

| Target | Uncovered code paths | Scenario |
|---|---|---|
| PyPSA | unit commitment on a conversion Link (`committable`, start/stop costs, min up/down time, ramps, `stand_by_cost`), `active: false`, `build_year`/`lifetime`, `p_nom_min`, `p_max_pu`, `marginal_cost_quadratic` on Links | `pypsa_link_uc` |
| PyPSA | `active`, `build_year`/`lifetime`, `p_nom_min`, `p_nom_mod`, `marginal_cost_quadratic` on StorageUnits; `e_sum_min` and `down_time_before` on Generators; `active` on Loads and Stores; Store `e_initial` on a fixed energy capacity | `pypsa_storage_extras` |
| PyPSA | `g`, `s_nom_min`, `s_nom_mod`, `active` on Lines; `s_nom_min`/`s_nom_max`/`s_nom_mod`/`s_max_pu`/`active` on Transformers; `investment_period` on a per-period emission limit | `pypsa_lopf_extras`, `pypsa_multi_period` (extend) |
| Calliope 0.7 | `cost_om_annual_investment_fraction`, `flow_cap_min_systemwide`, `timestep_weights`, `source_use_equals` (must-run), `purchased_units_min`, `active: false`, `one_way`, `force_async_flow`, `init.subset`, `shadow_prices`, `scoring_algorithm`; operate caps `area_use`, `source_cap`, `storage_cap` | `calliope_extensions` (extend), `calliope_operate_caps` |
| Calliope 0.6 | `energy_ramping`, `force_asynchronous_prod_con`, `one_way`, `units_min`, `resource_cap_max`, `om_annual_investment_fraction`, `resource_area_per_energy_cap`, `storage_cap_equals`, `carrier_out_3`, `subset_time`; group constraints `carrier_con_min/max/equals`, `carrier_prod_equals`, `cost_min`, `cost_equals` | `calliope06_extensions`, `calliope06_group_constraints` (extend) |
| AdOpT | `HeatPump_AirSourced` from `performance.model: heat_pump` | `adopt_heat_pump` |

### 2.3 Defect fixes

- PyPSA StorageUnit: apply `capacity.max` (`p_nom_max`), `operation.min_pu`
  and `operation.max_pu` (`p_min_pu`/`p_max_pu`, scalar and series) to the
  StorageUnit row, as Generators and Links already get them. Until then the
  validator rejects the combination instead of dropping it.
- AdOpT battery: write `Performance.performance.eta_in`/`eta_out` from
  `storage.charge_eff`/`discharge_eff` into the override sidecar, and until
  that lands change the reference cell from "DB performance" to a warning.
  `storage.self_discharge` and `storage.max_hours`, which the reference
  already marks as unmapped on AdOpT, are Phase 1 work (§3.4).

## 3. Phase 1: additive capability (0.2.x)

Each subsection names the canonical addition, the native parameter it reaches
per target, and the gate. "—" means the target has no slot and the field is
ignored with a warning, as today for the PyPSA-only fields
([ADR-0011](decisions/0011-pypsa-optimization-completeness.md)); "✗" means
the field is rejected there because ignoring it would change the problem.

### 3.1 Run-level passthrough: `experiment.native.<target>`

The block exists in the schema and is marked reserved. Honoring it is the
single largest step on the config side, because every run-level knob the
measurement found missing lands there without a canonical field:

| Target | Merged into | Covers |
|---|---|---|
| PyPSA | keyword arguments of `n.optimize(...)` and `n.pf(...)` in `run.py` (`_native.pypsa.json` becomes read) | `transmission_losses`, `linearized_unit_commitment`, `committable_big_m`, `meshed_thresholds`, `assign_all_duals`, `compute_infeasibilities`, `slack_weights` |
| Calliope 0.7 | `config.init` / `config.build` / `config.solve` | `backend`, `objective`, `distance_unit`, `retain_inactive`, `pre_validate_math_strings`, `postprocessing_active`, `solver_io`, `zero_threshold`, `spores.save_per_spore_path`, `score_threshold_factor`, `use_latest_results`, `tracking_parameter` |
| Calliope 0.6 | `run.*` and `model.*` | `backend`, `bigM`, `objective`, `relax_constraint`, `save_per_spore*`, `skip_cost_op`, `solver_io`, `zero_threshold`, `reserve_margin`, `group_share` |
| AdOpT | `ConfigModel.json` (today `model.native` does this; `experiment.native` is the better home for run options and `model.native` stays for compatibility) | all `solveroptions.*` tuning keys, `optimization.timestaging`, `typicaldays.method`, `monte_carlo.type`, `scaling.*`, `reporting.write_solution_diagnostics`, `economic.global_simple_capex_model` |

Rules: keys are merged last and verbatim, like every `native` block; a key
MEME itself sets (mode, solver name, horizon) is overridden with a warning
naming the key, so a consumer sees the override. Gate: none, the block is
scoped by target key already.

### 3.2 Solver and objective

| Canonical | PyPSA | Calliope 0.7 | Calliope 0.6 | AdOpT | Gate |
|---|---|---|---|---|---|
| `solver.big_m` | `committable_big_m` | `bigM` | `run.bigM` | — | none |
| `solver.name: gurobi` honored | ✓ today | ✓ today | ✓ today | `solveroptions.solver: gurobi` instead of forced `glpk` (glpk stays the default; ADR-0007 is about what the image ships, not what a consumer may request) | none |
| `solver.name: highs` on Calliope 0.7 | — | `config.build.backend: highs` when no piecewise math is rendered, else `cbc` with the existing warning | stays `cbc` | — | none |
| `objective: min_emissions` | objective swap in `run.py`: minimize Σ `co2_emissions` × dispatch / efficiency via linopy, monetary cost as a reporting column | `objective_cost_weights: {monetary: 0, co2: 1}` (MEME already tracks the `co2` class) | ✓ today | ✓ today | feature `objective:min_emissions` claimed by all |
| `model.carbon_price` (scalar or series; optional `subsidy`) | added to `marginal_cost` as intensity × price per generator | a priced `co2` cost class (`objective_cost_weights[co2] = price`) | `costs.emissions` weight in `objective_options.cost_class` | `CarbonCost.csv` `price`/`subsidy` (today written empty) | none; `emission_limits` and a price may coexist |

### 3.3 Time

| Canonical | PyPSA | Calliope 0.7 | Calliope 0.6 | AdOpT |
|---|---|---|---|---|
| `time.subset` | snapshot subset in `run.py` (closes a ✗) | ✓ today | ✓ today | ✗ stays (needs a regular range) |
| `time_aggregation.method: cluster` (`periods` = clusters, `cluster_series`, `keep_full_resolution_for`) | ✗ stays (no native clustering) | `config.init.time_cluster` from a MEME-computed cluster file plus the `storage_inter_cluster` math (`cluster_first_timestep`, `lookup_*`, `timestep_cluster`, `include_storage`) | `model.time.function: apply_clustering` with `clustering_func: kmeans` on `cluster_series` (today only `typical_days`) | `typicaldays.N` with `typicaldays.method` selected (`1` precomputed from `cluster_series`, `2` k-means) |
| `time_aggregation.params.method` for `typical_days` | — | — | — | `typicaldays.method` |

The clustering itself (k-means over `cluster_series`) runs in MEME so that all
targets see the same representative days, which is what makes the
cross-target objective comparison meaningful.

### 3.4 Storage

| Canonical | PyPSA | Calliope 0.7 | Calliope 0.6 | AdOpT |
|---|---|---|---|---|
| `storage.energy_per_unit` (+ `capacity.units_*`) | Store `e_nom_mod` | `storage_cap_per_unit` | `storage_cap_per_unit` | `size_is_int: 1` with `Units.size` |
| `storage.min_power_to_energy` | — (no slot; Store links get a lower bound in `_storage.json`) | `flow_cap_per_storage_cap_min` | `energy_cap_per_storage_cap_min` | — |
| `storage.soc_max` (scalar or series) | Store `e_max_pu` | — | — | — |
| `storage.soc_profile` on a Store | `e_set` (today StorageUnit `state_of_charge_set` only) | — | — | — |
| `storage.cyclic_per_period` | `e_cyclic_per_period`, `cyclic_state_of_charge_per_period`, `state_of_charge_initial_per_period` | — | — | — |
| `costs.monetary.variable_om_storage` | `marginal_cost_storage` | — | — | — |
| `storage.charge_eff`/`discharge_eff` on AdOpT | ✓ today | ✓ today | ✓ today | `eta_in`/`eta_out` (Phase 0 fix) |
| `storage.self_discharge` on AdOpT | ✓ today | ✓ today | ✓ today | `Performance.performance.lambda` |
| `storage.max_hours` on AdOpT | ✓ today | ✓ today | ✓ today | `Flexibility.power_energy_ratio` |
| `costs.monetary.investment_per_capacity` on AdOpT storage | ✓ today | ✓ today | ✓ today | `Flexibility.capex_charging_power`/`capex_discharging_power` (power capex; `unit_CAPEX` stays the energy capex) |
| `storage.inflow` on AdOpT | ✓ today | — | — | `Hydro_Open`/`PumpedHydro_Open` with the `<tech>_hydro_inflow` climate column (closes `performance.model: open_hydro`) |
| storage in a power flow: `dispatch` with sign, `electrical.control: pv` | `p_dispatch_set`/`p_store_set`, `control` on StorageUnits | — | — | — |

The PyPSA-only rows are ignorable fields in the sense of ADR-0011 and need
no new feature key; `energy_per_unit` and `min_power_to_energy` are shared
concepts and become first-class.

### 3.5 Technologies and operation

| Canonical | PyPSA | Calliope 0.7 | Calliope 0.6 | AdOpT |
|---|---|---|---|---|
| `area.min`, `costs.monetary.area` | ✗ gated as today | `area_use_min`, `cost_area_use` | `resource_area_min`, `costs.resource_area` | ✗ gated |
| `area.per_capacity` on 0.7 | ✗ gated | `area_use_per_flow_cap` (today warned and dropped) | ✓ today | ✗ gated |
| `source.min`, `source.efficiency`, `source.cap_min`, `costs.monetary.source_capacity` | ✗ gated | `source_use_min`, `source_eff`, `source_cap_min`, `cost_source_cap` | `resource_min_use`, `resource_eff`, `resource_cap_min`, `costs.resource_cap` | ✗ gated |
| `demand_unit: per_cap \| per_area \| absolute` | — | `sink_unit` | `resource_unit` on the demand tech | — |
| `parasitic_efficiency` | folded into `efficiency` (warned) | `flow_out_parasitic_eff` | `parasitic_eff` | — |
| `capacity.units_systemwide_min/max` | sidecar constraint on `p_nom_mod` units | `purchased_units_min/max_systemwide` | `units_min/max_systemwide`, `units_equals_systemwide` | — |
| `annual_output_min/max` on Calliope 0.7 | ✓ today | custom math on `flow_out` summed over time (today ✗) | ✓ today | — |
| `operation.min_pu` on AdOpT | ✓ today | ✓ today | ✓ today | `Performance.min_part_load` |
| `operation.equals_pu` / must-run on AdOpT RES | ✓ today | ✓ today | ✓ today | `Performance.curtailment: 0` (default `1`) |
| `operation.initial_dispatch` | `p_init` for the rolling horizon | — | — | — |
| `costs.monetary.purchase` on AdOpT | ✗ stays | ✓ today | ✓ today | `Economics.fix_CAPEX` (`CAPEX_model: 2`) |
| `costs.monetary.investment_piecewise` (breakpoints) | ✗ | piecewise cost math on `flow_cap` | ✗ | `Economics.piecewise_CAPEX` (`CAPEX_model: 3`) |
| `carriers.<id>.color` emitted | `carriers.csv` `color` | — | `essentials.color` | — |
| `flows` with several inputs on 0.6 | ✓ today | ✓ today | `carrier_in_2`/`_3`, `primary_carrier_in` with `carrier_ratios` (today rejected) | see §3.9 |

### 3.6 Transmission and the electrical network

| Canonical | PyPSA | Calliope 0.7 | Calliope 0.6 | AdOpT |
|---|---|---|---|---|
| `transmission` on AdOpT | ✓ today | ✓ today | ✓ today | `Networks.json` + `network_topology/{existing,new}/{connection,distance,size,size_max_arcs}.csv`; database network chosen by carrier (`electricitySimple`, `hydrogenSimple`, `heat`, `CO2_Pipeline`), economics and losses overridden from the arc (today rejected; closes the `Networks unmapped` ✗) |
| `transmission.delay` (+ `cyclic`) | Link `delay`/`cyclic_delay` | — | — | — |
| `carrier` and `length` on every arc | `carrier` on Links and Lines, `length` on Links | ✓ today | ✓ today | ✓ via network |
| `transmission.electrical.angle_limit` | `v_ang_min`/`v_ang_max` | — | — | — |
| `transmission.electrical.tap_side`, `tap_position` | Transformer `tap_side`, `tap_position` | — | — | — |
| `transmission.electrical.b`/`g` on transformers | Transformer `b`/`g` (today lines only) | — | — | — |
| `transmission.library: {"pypsa": "<LineType or TransformerType>"}` (see §3.8 for the field) | `type` on Lines/Transformers; `r`/`x`/`b`/`s_nom` derived by PyPSA from the standard type, explicit `electrical` values win | — | — | — |
| `node.electrical.v_min_pu`/`v_max_pu` | Bus `v_mag_pu_min`/`v_mag_pu_max`; the power-flow summary reports violations | — | — | — |
| `build_year`/`lifetime` on arcs and Stores | multi-period Lines, Transformers, Links and Stores (today Generators and StorageUnits only) | — | — | — |
| `experiment.network.losses: {segments: n}` | `transmission_losses` | — | — | — |
| `experiment.network.expansion: iterative` | `optimize_transmission_expansion_iteratively` | — | — | — |
| `experiment.power_flow.after_optimize: true` | `optimize_and_run_non_linear_powerflow` (mode plan, then a power flow on the result) | ✗ | ✗ | ✗ |
| `experiment.power_flow.contingency: [arcs]` | `lpf_contingency` | ✗ | ✗ | ✗ |
| `experiment.alternatives.directions` | `optimize_mga_in_direction` / `_in_multiple_directions` | — (SPORES scores instead) | — | ✗ |

The PyPSA-only run options are run specifications and are rejected on
transport targets like `linear_power_flow` is today; the per-arc electrical
fields are ignored with the existing one-line warning.

### 3.7 Constraints

| Canonical | PyPSA | Calliope 0.7 | Calliope 0.6 | AdOpT |
|---|---|---|---|---|
| IR variables `cost_var`, `cost_investment` | linopy expression over `marginal_cost` / `capital_cost` | custom math on `cost_var`/`cost_investment` | `cost_var_*`, `cost_investment_*` group constraints | ✗ |
| `constraints[].per_timestep: true` | one linopy constraint per snapshot | `foreach: [timesteps]` | `*_per_timestep_*` group constraints | ✗ |
| share constraints: `terms` of the form `flow_out[A] - share × flow_out[all] ≥ 0` recognised and emitted natively | linopy as today | custom math as today | `carrier_prod_share_*`, `demand_share_*`, `net_import_share_*`, `energy_cap_share_*` (today multi-term ✗) | ✗ |
| `emission_limits[].nodes` | GlobalConstraint `bus` | `where` clause on nodes | `locs` on the group | ✗ |
| `model.reserve_margin` | sidecar constraint on firm capacity vs peak load | custom math | `model.reserve_margin` | ✗ |

### 3.8 Technology catalogs: library entries and synthesis, selectable

Today four AdOpT database entries are reachable, by a fixed mapping the
consumer cannot see or steer. Two frameworks ship a catalog, AdOpT its
technology database (54 JSON files under
`adopt_net0/database/templates/technology_data/`, literature values
maintained upstream) and PyPSA its `LineType`/`TransformerType` tables, so a
catalog reference is a two-framework concept and becomes a canonical field
under the rule of [ADR-0003](decisions/0003-native-passthrough.md). The
consumer chooses, per element and as a default, between **a library entry**
(an upstream-validated definition, economics overridden from the payload)
and **a synthesized definition** (written by MEME from the canonical fields
alone).

#### The field: `library`

```jsonc
"technologies": {
  "h2_store": { "role": "storage", "carrier_out": "hydrogen", "…": "…",
                "library": { "adopt-net0": "Storage_H2_Cavern" } },   // pin this entry
  "ccgt":     { "role": "conversion", "carrier_in": "gas", "carrier_out": ["electricity", "heat"], "…": "…",
                "library": { "adopt-net0": null } },                  // pin synthesis for this tech
  "pv":       { "role": "supply", "performance": { "type": "physics", "model": "pv" }, "…": "…" }
                                                                        // no pin: the policy decides
},
"transmission": {
  "l1": { "from": "n1", "to": "n2", "carrier": "electricity", "electrical": { "kind": "line" },
          "library": { "pypsa": "NAYY 4x50 SE" } }                      // a PyPSA standard type
}
```

- A **name** pins one catalog entry of that target. The name must exist in
  the pinned framework release (MEME carries the name list per target
  version, as it carries the feature matrix); an unknown name is a 422 that
  lists the closest matches.
- **`null`** pins synthesis for that element on that target.
- **Absent** means the policy below decides. Keys for targets not selected
  are ignored with the warning `native` blocks get today.

#### The policy: `experiment.catalog`

| Value | Behavior on AdOpT (and on PyPSA arcs with an `electrical` block) |
|---|---|
| `auto` (default) | explicit pin → matched library entry (§3.8.1) → synthesized definition (§3.8.2); every synthesized or matched element is named in `warnings` with the entry or file it became |
| `library` | explicit pin → matched library entry → **422** naming the element and the database categories it could not be matched to; identical to today's behavior for the four mapped techs, so no payload that validates today changes |
| `synthesize` | explicit pin → synthesized definition for every element MEME can synthesize; physics-model techs (`pv`, `wind`, `heat_pump`, `open_hydro`) have no generic class and still take their library entry, with a warning |

An explicit pin always wins over the policy in both directions, so one
payload can carry a validated library battery next to a synthesized
turbine. Default `auto` turns a 422 into a run for payloads that were
rejected before, which is additive; a consumer who wants the strict
behavior sets `library`.

#### 3.8.1 Library matching

Where the policy or the pin asks for a library entry and no name is given,
MEME matches on canonical fields (no schema change beyond `library`):
storage by carrier (`electricity` → `Storage_Battery`, `hydrogen` →
`Storage_H2` or `Storage_H2_Cavern` by `capacity.max`, `heat` →
`Storage_HotWater`, `gas` → `Storage_NG`); heat pumps by
`performance.params.source` (`air`, `ground`, `water`, `sea`); wind
turbines by `performance.params.offshore` and `capacity.per_unit`
(1500/2500/4000 kW onshore, 6000/9500/11000 kW offshore); solar thermal from
`performance.model: solar_thermal` (new enum value, a `source` on Calliope
and a precomputed profile elsewhere); boilers and furnaces from a conversion
`fuel → heat` by fuel carrier and size; gas and hydrogen turbines, the
combined cycle and the steam turbine from a conversion `fuel → electricity
(+ heat)` by fuel carrier and `capacity.per_unit`. The entry is copied by
`copy_technology_data` from the package database as today and patched with
the override sidecar; performance curves and scaling factors the payload
does not express stay upstream's values. The match is reported in
`warnings` (`tech "ccgt": library entry GasTurbine_NG_250 (fuel gas,
250 MW)`), so the choice is never silent.

#### 3.8.2 Synthesis

Where the policy or the pin asks for synthesis, MEME writes the technology
JSON itself. AdOpT selects the model class from the `tec_type` field, not
from the file name, and its generic classes accept any structurally valid
file: `CONV1` (one input, one output), `CONV2` (several inputs, one output),
`CONV3` (one input, several outputs at fixed ratios), `STOR`, `RES` and
`SINK`; 17 of the 54 upstream entries are plain `CONV*` files that differ
only in their numbers.

- **Values from the payload:** `efficiency` or the `flows` ratios as the
  performance `in`/`out`, `capacity.min/max/existing`,
  `investment_per_capacity` as `unit_CAPEX`, `fixed_om`, `variable_om`,
  `lifetime`, `interest_rate`, `emission_factor`, `operation.min_pu` as
  `min_part_load`, ramps and the unit-commitment fields as `ramping_time`,
  `SU_time`/`SD_time`, `SU_load`/`SD_load`, `min_uptime`/`min_downtime`,
  `max_startups`, `standby_power` (with `performance.dynamics: 1`); for
  storage `charge_eff`/`discharge_eff` as `eta_in`/`eta_out`,
  `self_discharge` as `lambda`, the rate bounds and `max_hours`.
- **Performance function type from the performance block:** constant → `1`,
  constant with `min_pu` → `2`, `piecewise` → `3`, `piecewise` with
  `min_pu` → `4`. This is what makes `performance.type: piecewise` reachable
  on AdOpT.
- **Neutral, documented defaults** for what the canonical model does not
  express: `CAPEX_model: 1`, `size_is_int: 0` (or `1` with `capacity.per_unit`),
  `decommission: impossible`, the `Units` metadata.
- **Structure** from an upstream file of the same class
  (`GasTurbine_simple.json` for `CONV*`, `Storage_Battery.json` for `STOR`),
  so the file matches the pinned release's schema; a schema change upstream
  shows up as a golden diff.

Synthesized files are named `meme_<tech id>.json`, listed under that name in
`Technologies.json` and written to `_meme_technology_data/` in the input
tree; `run.py` copies the library entries as today, then the synthesized
files, and the override step patches both alike. Every synthesized file is
frozen by the golden tree and named in `warnings`
(`tech "ccgt": synthesized as meme_ccgt.json (CONV3, performance type 2)`).
Synthesis claims `multi_carrier_conversion`, `committable`,
`piecewise_performance` and `role: conversion` for AdOpT and turns
`fuel_cost` from ✗ into an import price on the fuel carrier.

Fixed supply profiles (`operation.equals_pu` series on a supply tech
without a physics model) need no file at all: they become the `Generic
production` column of the carrier CSV.

#### 3.8.3 Outside the canonical vocabulary

CCS (`MEA_*`, `CO2_Compressor`), `DAC_Adsorption`, the industrial chain
(`CrackerFurnace*`, `EthyleneCompression*`, `EthyleneSeparation`,
`SteamReformer*`), `PermanentStorage_CO2_simple`, `TemporaryStorage_CO2`,
`Storage_Ethylene`, `Storage_PCM`, and per-tech `ScalingFactors`, `T_out`,
`application` stay reachable only through node-level `native.adopt-net0`.
A `library` pin to one of these names is accepted when the canonical role
fits the entry's carriers, so a consumer can still select them without the
native block; the override sidecar then carries the economics as for any
other entry.

The `library` field and the matching (§3.8.1) can ship early (M3); the
synthesis (§3.8.2) is a design decision, because MEME then puts technology
definitions into circulation that upstream never validated, and gets its own
ADR before implementation (M5). The ADR fixes the `meme_` prefix, the golden
freeze, the provenance warnings and the default of `experiment.catalog`.

### 3.9 Multi-period on AdOpT

`model.periods` with years maps to `Topology.json` `investment_periods` and
`investment_period_length`, one `period<n>` folder per period with the
series repeated, and `optimization.multiyear: 1`; `build_year` and
`lifetime` drive `existing` vs `new`. The ⚠ for `multi_period` on AdOpT
becomes ✓. Calliope has no native multi-period and keeps the warning.

### 3.10 Time series from files

`timeseries.source: file` is schema-valid and rejected by every target.
Additive completion: a job may carry a `files` map (`name → base64` or a
multipart upload on `/simulate`), and `path`/`column` resolve against it.
If this is not built by the `0.3.0` cut, the field is removed there
([§4](#4-phase-2-breaking-changes-for-030)).

## 4. Phase 2: breaking changes for 0.3.0

Collected, each with the `0.2.x` deprecation that precedes it.

| Change | Why it breaks | Deprecation in 0.2.x |
|---|---|---|
| Remove the legacy `experiment.solver.options.{mga, spores, operate, monte_carlo, pareto}` namespaces | payloads that still use them stop validating | already warned; the warning names the typed block to use |
| Remove `timeseries.source: file` unless §3.10 shipped | a schema-valid value disappears | warning on every payload that uses it |
| `experiment.foresight`: either implement `myopic` (PyPSA per-period optimization, Calliope 0.6 `use_cap_results` chains) or remove the field | accepted today without effect | warning "accepted, not mapped" becomes "will be removed" |
| `node.allowed_techs`: enforce (reject a technology placed on a node that does not allow it) or remove | informational today; enforcing rejects payloads that validate now | warning when a placement contradicts it |
| `capacity.decommission.cost` on PyPSA: price it through a linopy term instead of ⚠ | changes the objective of existing payloads that carry a cost | warning already names the drop |
| PyPSA cost representation: emit `overnight_cost`, `discount_rate`, `lifetime`, `fom_cost` and let PyPSA annualize, instead of a pre-computed `capital_cost` | the emitter contract (golden bytes) changes for every PyPSA payload; results stay equal within rounding, multi-period gets exact per-period annuities | none needed for consumers; goldens are regenerated with the release and the changelog explains the representation change |
| Calliope 0.7 default backend `highs` where no piecewise math is rendered | solver change for payloads that said `highs` and silently got `cbc` | warning "highs will be honored from 0.3.0" |
| AdOpT honors `solver.name: gurobi` | payloads that named gurobi and got glpk start requiring a licence | warning "gurobi will be honored from 0.3.0" |
| `storage.max_hours` on PyPSA becomes a Store with charger and discharger when `energy_capacity` is absent but `soc_max`, `cyclic_per_period` or `variable_om_storage` is set | representation switch for those payloads | warning naming the switch |
| Plan-to-operate chaining: `experiment.operate.capacities_from: <job id>` reads the capacities of a finished job (PyPSA `p_nom_set`/`fix_optimal_capacities`, Calliope 0.6 `use_cap_results`, 0.7 `flow_cap` inputs) | amends ADR-0008's "MEME never chains jobs" and gives the API a dependency between jobs | new ADR; the field is reserved (rejected with a pointer) in 0.2.x |

Everything else in this plan is additive and must not wait for `0.3.0`.

## 5. Not planned

Listed so that nobody searches for them in the schema:

- PyPSA internals and bookkeeping: `type` (except the standard-type reference
  of §3.6), `sign`, `p_nom_set`/`s_nom_set`/`e_nom_set` (results of a
  previous optimize, covered by chaining), `unit`, `location`, `nice_name`,
  `Shape`, `SubNetwork`; the `Process` component is watched as the future
  home of `flows` once PyPSA documents it as stable.
- Calliope scaling and formatting: `energy_cap_scale`, `resource_scale`,
  `resource_cap_equals_energy_cap`, `date_format`, `datetime_format`,
  `calliope_version` (emitted as the pinned version, not configurable),
  `file_allowed`, `timeseries_dateformat`, the deprecated `charge_rate` alias,
  tech-level export (`carrier_export`, `cost_export`, `export_*`): a
  single-framework concept; `trade.export` stays the portable form.
- AdOpT solver tuning, scaling factors and the industrial/CCS/DAC
  technologies: through `experiment.native` and node-level `native`.

## 6. Sequencing

| Milestone | Content | Release | Risk |
|---|---|---|---|
| M1 | §2 Phase 0: attribute-matrix guard, scenarios for every uncovered code path, the two defect fixes, reference corrected | `0.2.1-alpha` | low; no schema change |
| M2 | §3.1 run-level passthrough, §3.2 `big_m`, carbon price, `min_emissions` on PyPSA and Calliope 0.7, `time.subset` on PyPSA | `0.2.2-alpha` | low; passthrough is merge code, the objective swap needs E2E pins |
| M3 | §3.4 and §3.5: storage fields, area/source completions, AdOpT storage and operation parameters; `library` field, `experiment.catalog` with `auto` and `library`, library matching (§3.8.1) | `0.2.3-alpha` | medium; AdOpT E2E for every new database tech |
| M4 | §3.6 and §3.7: AdOpT networks, PyPSA network options and electrical extras, constraint IR variables and share constraints, multi-period arcs and Stores | `0.2.4-alpha` | medium; Kirchhoff and multi-period goldens |
| M5 | §3.8.2 synthesis and `experiment.catalog: synthesize` (after its ADR), §3.3 clustering, §3.9 multi-period on AdOpT, §3.10 files | `0.2.5-alpha` | high; new ADRs, cross-target consistency tests for clustering |
| M6 | §4 as one release, deprecations announced in M2–M5 | `0.3.0` | API break by design |

Every milestone ends with `make test`, the golden update, the E2E tier in the
container and a capability-reference edit, so the reference keeps mirroring
the enforced behavior.

## 7. Reproducing the measurement

The denominators come from the pinned releases inside the environment image:

```bash
# PyPSA input attributes per component
docker run --rm -i meme-env:dev python -c \
  'import pypsa; n=pypsa.Network(); [print(c, list(n.components[c].defaults.query("status.str.startswith(\"Input\")").index)) for c in sorted(n.all_components)]'
# Calliope 0.7 math parameters: calliope/math/*.yaml "parameters" keys (ruamel.yaml in /opt/calliope-0.7-venv)
# Calliope 0.6 parameters: calliope/config/defaults.yaml via AttrDict.from_yaml (/opt/calliope-0.6-venv)
# AdOpT: adopt_net0.data_preprocessing.create_optimization_templates(<dir>) and database/templates/**/*.json
```

The numerator is the union of what the emitters write for the whole corpus.
Run the service dry on a free port (a port that is already bound makes the
binary exit while `curl` happily talks to whatever else listens there) and
convert every payload per target:

```bash
go build -o /tmp/meme ./cmd/meme
/tmp/meme -addr 127.0.0.1:18555 -work /tmp/emit -env-file /nonexistent &
for f in internal/scenarios/testdata/scenarios/*.json examples/*.json; do
  for t in pypsa calliope calliope-0.6 adopt-net0; do
    curl -s -X POST "127.0.0.1:18555/convert?target=$t" -H 'Content-Type: application/json' --data-binary @"$f" > /dev/null
  done
done
```

Then union the CSV headers and `<component>-<attr>.csv` names (PyPSA), the
YAML keys and data-table parameter names (Calliope), and the `ConfigModel`,
override and `Technologies.json` content (AdOpT) under `/tmp/emit`, and diff
against the denominators. §2.1 turns this into a test so the diff is
maintained rather than repeated.
