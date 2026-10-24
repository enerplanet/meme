# Scenario catalog


Every payload in [`internal/scenarios/testdata/scenarios/`](https://github.com/enerplanet/meme/tree/main/internal/scenarios/testdata/scenarios/)
is a self-contained `Job` that must **validate + emit cleanly** for its target;
`TestScenarios` asserts the emitted tree contains the expected framework-native
markers, and `TestScenariosCapabilityGating` asserts each target-specific mode is
rejected on the targets that can't honor it. All outputs are shaped to be
directly loadable by the pinned framework (see [Targets and versions](targets.md)), so "emits cleanly" means
"runnable".

## PyPSA

| Scenario | Exercises | Key emitted markers |
|---|---|---|
| `pypsa_generators` | supply generators, capex/opex | `p_nom_min`, `capital_cost`, `marginal_cost` |
| `pypsa_conversion` | multi-output CHP Link | `bus2`, `efficiency2` |
| `pypsa_storage` | StorageUnit detail | `efficiency_store`, `standing_loss`, `cyclic_state_of_charge` |
| `pypsa_transmission` | inter-node Link | `n1::electricity`, `n2::electricity` |
| `pypsa_trade` | import/export market gens | `market_import`, `market_export` |
| `pypsa_committable` | unit commitment | `committable`, `start_up_cost`, `ramp_limit_up` |
| `pypsa_emission` | CO₂ cap GlobalConstraint | `primary_energy`, `co2_emissions` |
| `pypsa_constraints` | linear-constraint IR | `max_pv` |
| `pypsa_extensions` | must-run dispatch (`operation.equals_pu` pins both bounds), a system-wide capacity cap (`capacity.systemwide_max`), an import limited by a series (`trade.import.limit`) | `p_min_pu` = `p_max_pu`, `p_nom_max`, `backup_import` |
| **`pypsa_node_override`** | one tech on `[n1,n2]` with per-node capacity/cost overrides | `pv@n1`, `pv@n2`, per-node `p_nom_max` |
| **`pypsa_physics_precomputed`** | physics `pv` model carried by a precomputed CF series | `_performance.json` (`type: physics`, `precomputed`) |
| `pypsa_operate` | rolling-horizon dispatch over fixed capacities (`optimize_with_rolling_horizon`) | `gen`, fixed `p_nom` |
| `pypsa_alternatives` | MGA near-optimal alternative (`optimize_mga`; the payload still uses the deprecated `solver.options.mga` namespace and validates with a warning) | `p_nom_extendable` |
| `pypsa_stochastic` | two-stage stochastic (`set_scenarios` + per-scenario overrides) | `_scenarios.json` (`fuel_high`, translated `marginal_cost`) |
| `pypsa_constraints_sidecar` | tech-scoped constraint applied by run.py as a linopy constraint (binding, objective-checked in E2E) | `force_pv` in `_constraints.json` |
| `pypsa_trade_price_series` | time-varying import price (objective-checked in E2E) | `market_import`, `generators-marginal_cost.csv` |
| `pypsa_power_flow` | mode `power_flow`: an HV slack feeding two MV nodes through a transformer and a line, PV with a dispatch series, two loads (converges 4/4 in E2E; the linear method and a diverging load are E2E-checked too) | `v_nom`, `lines.csv`, `transformers.csv`, `control: Slack`, `generators-p_set.csv` |
| `pypsa_lopf` | plan under the voltage law (`network.model: linear_power_flow`): a 110 kV ring, an expandable priced line with `max_loading`; E2E checks Kirchhoff's law around the loop and the security-constrained variant | `s_nom_extendable`, `s_max_pu`, `capital_cost` on `lines.csv` |
| `pypsa_store` | energy-sized storage as a Store with charger/discharger and the `_storage.json` constraints; a pumped unit with a pinned `soc_profile` | `stores.csv`, `battery_charge`, `equal_power`, `state_of_charge_set` |
| `pypsa_modular` | integer modules on a generator and a store | `p_nom_mod`, `e_nom_mod` |
| `pypsa_retire_slack` | continuous retirement of an old unit; priced slack on an island short of capacity | `::unmet`, retirable `old` |
| `pypsa_copperplate` | copperplate: a free hub joins the nodes, the arc is dropped | `copperplate::electricity` |
| `pypsa_uc_extras` | start/stop ramps, standby cost, prior uptime on a committable unit | `ramp_limit_start_up`, `stand_by_cost`, `up_time_before` |
| `pypsa_quadratic` | quadratic variable cost (QP on HiGHS) | `marginal_cost_quadratic` |
| `pypsa_multi_period` | two investment periods: a 2020 unit retires before 2040, new capacity is built per period | `investment_periods.csv`, `new-2030`, `new-2040`, `build_year` |
| `pypsa_power_flow_reactive` | reactive power and voltage control: slack set-point 1.02 pu, a `pv` generator holding 1.0 pu, loads with a power factor and a reactive series, a shunt, a per-length line impedance, a phase-shifting transformer, a controlled HVDC link into an island PyPSA balances itself (E2E pins the held voltages) | `v_mag_pu_set`, `control: PV`, `q_set`, `shunt_impedances.csv`, `phase_shift`, links `p_set` |

## Calliope 0.7 (`calliope`)

| Scenario | Exercises | Key emitted markers |
|---|---|---|
| `calliope_plan` | base plan run | `mode: base`, `base_tech: supply`, `solver: cbc` |
| `calliope_spores` | SPORES / MGA | `mode: spores`, `number:` |
| `calliope_operate` | receding-horizon dispatch | `mode: operate` |
| `calliope_custommath` | constraint IR + emission limit → `additional_math.yaml` | `extra_math`, `max_pv`, `cost_flow_in` |
| `calliope_storage` | storage tech | `base_tech: storage`, `storage_cap_max`, `cost_storage_cap` |
| `calliope_source_native` | first-class `technology.source` + `area` (`source_use_max`, `source_unit`, `area_use_max`) | `source_use_max`, `area_use_max` |
| **`calliope_conversion`** | multi-output CHP → carrier lists + ratio-pinning extra math (rigid ratios, single-counted fuel) | `meme_ratio_chp_out_heat`, masked `balance_conversion` |
| **`calliope_transmission`** | transmission tech across two nodes | `base_tech: transmission`, `link_from`, `link_to` |
| **`calliope_node_override`** | per-node `cost_flow_cap` override under `nodes.<n>.techs` | `cost_flow_cap`, overridden value |
| `calliope_trade` | import/export market techs (import = supply at price + co2 factor; export = flexible demand paid the price) | `market_import`, `market_export`, `cost_flow_in: -30` |
| `calliope_committable` | MILP unit commitment (integer units, min stable load) | `cap_method: integer`, `flow_cap_per_unit`, `extra_math: [milp]` |
| `calliope_resample` | `time_aggregation: resample` → `config.init.resample.timesteps` | `resample:`, `timesteps: 3h` |
| `calliope_piecewise` | piecewise part-load curve → `piecewise_constraints` math on a fixed-capacity conversion | `piecewise_x`, `breakpoints` |
| **`calliope_multi_node`** | one demand tech at two nodes → one data table with a column per node (#7); solves to the same objective on PyPSA and both Calliope releases (`TestE2EMultiNodeDemandConsistency`) | `techs,load,load`, `nodes,n1,n2` |
| **`calliope_multi_node_supply`** | one PV tech with an availability series at two nodes → `source_use_max` table with a column per node (#7, supply side); same-objective check on all three targets (`TestE2EMultiNodeAvailabilityConsistency`) | `techs,pv,pv`, `source_unit: per_cap` |
| **`calliope_operate_limits`** | operate mode with a limited import and a capacity-bound line (#8): both installed as fixed `flow_cap`; same-objective check on PyPSA and both Calliope releases (`TestE2EOperateLimitsConsistency`) | `flow_cap: 50`, `flow_cap: 40` |
| `calliope_extensions` | annualized costs under a model-wide `discount_rate`, a system-wide capacity cap, a battery with `energy_capacity`, depth of discharge and charge rate, a curtailable demand (`demand_curtailable`), `reporting.save_logs` | `flow_cap_max_systemwide`, `storage_discharge_depth`, `flow_cap_per_storage_cap_max`, `sink_use_max`, `save_logs` |
| `calliope_hub_node` | a hub node without technologies joined by two arcs (0.7 and 0.6 dialects) | `link_to: hub`, `link_from: hub`, `hub:` with `techs: {}` |

## Calliope 0.6 (`calliope-0.6`)

The 0.6 target runs every `calliope_*` scenario its gates accept (all of the
above except `calliope_piecewise`, whose piecewise performance has no 0.6
representation) through its own dialect — `TestScenarios` pins the 0.6
markers, the goldens under `testdata/golden/calliope-0.6/` freeze the emitted
documents, and `TestE2ECalliope06Corpus` solves them on the real `calliope-0.6`
CLI — plus four 0.6-specific scenarios:

| Scenario | Exercises | Key emitted markers |
|---|---|---|
| **`calliope06_typical_days`** | `time_aggregation: typical_days` → k-means representative days (0.7 dropped built-in clustering) | `apply_clustering`, `clustering_func: kmeans`, `k: 2`, `random_seed` |
| **`calliope06_spores`** | first-class `experiment.alternatives` → `run.spores_options` + `systemwide_cost_max` group + `spores_score` costs | `mode: spores`, `spores_number: 2`, `slack: 0.1` |
| **`calliope06_group_constraints`** | constraint IR + emission limit → `group_constraints` (capacity cap, minimum production, CO₂ cap) | `energy_cap_max: 800`, `carrier_prod_min`, `cost_max: {emissions: …}` |
| **`calliope06_min_emissions`** | `objective: min_emissions` → `objective_options.cost_class` weights | `monetary: 0`, `emissions: 1` |

## AdOpT-NET0

| Scenario | Exercises | Key emitted markers |
|---|---|---|
| `adopt_demand_trade` | demand + import/export carrier CSVs | `;Demand;`, `glpk` |
| `adopt_storage` | `Storage_Battery` database tech + overrides | `Storage_Battery`, `OPEX_fixed` |
| `adopt_pv_climate` | physics `pv` → `Photovoltaic` + `ClimateData.csv` | `Photovoltaic`, `;ghi;` |
| `adopt_emission` | emission-limit objective | `costs_emissionlimit` |
| **`adopt_wind`** | physics `wind` → `WindTurbine_Onshore_1500` + wind climate columns | `WindTurbine_Onshore_1500`, `;ws10;` |
| **`adopt_pareto`** | `mode: pareto` → `objective: pareto` (+ `points` via the deprecated `solver.options.pareto`, warned) | `pareto` |
| **`adopt_monte_carlo`** | `mode: monte_carlo` → `monte_carlo.N > 0` (via the deprecated `solver.options.monte_carlo.N`, warned) | `monte_carlo` (N asserted in `TestAdoptRunModeConfig`) |
| **`adopt_typicaldays`** | `time_aggregation: typical_days` → `typicaldays.N = periods` | `typicaldays` (N asserted in `TestAdoptRunModeConfig`) |
| `adopt_node_override` | per-node storage overrides in the node-keyed `_meme_overrides.json` | `Storage_Battery`, per-node `size_max` |
| `adopt_extensions` | a battery with independent charge/discharge rates and `no_simultaneous_charge_discharge`, retirement (`capacity.decommission`), import/export with emission factors, `discount_rate`, `reporting.case_name` | `charge_rate`, `discharge_rate`, `allow_only_one_direction`, `decommission`, `case_name` |
| `adopt_min_emissions` | `objective: min_emissions` on a PV system | `emissions_net`, `Photovoltaic` |

The three AdOpT run-mode scenarios are backed by
`TestAdoptRunModeConfig`, which parses the emitted `ConfigModel.json` and checks
the mode actually reached AdOpT's `optimization` block (objective / `monte_carlo.N`
/ `typicaldays.N`) — the translation described on the [targets page](targets.md) that makes those modes
genuinely runnable rather than silently downgraded to a cost-optimal plan.
