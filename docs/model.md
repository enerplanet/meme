# Canonical model


### The `Value` union

A parameter is one of three JSON shapes; emitters call `Reduce`/`Select` to
project an indexed value to a scalar (and error if it can't for a flat target).

```jsonc
0.9                                              // scalar
"pv_cf"                                           // time-series id (see timeseries)
{ "data": [10,20], "index": [["monetary"],["co2"]], "dims": ["costs"] }  // indexed
```

### Canonical payload — `Job`

```jsonc
{
  "api_key"?: "…",   // required iff the server has API_KEY configured; redacted from the bundle
  "model": {
    "metadata": { "name": "…", "currency"?: "EUR", "currency_year"?: 2025 },
    "time":     { "start": "2025-01-01", "end": "2025-01-02", "resolution"?: "1H", "subset"?: ["…","…"],   // subset: Calliope only
                  "timesteps"?: ["…"], "weights"?: Value },          // explicit labels (P/C); snapshot weights (P; C scalar)
    "periods"?: ["period1"] | [{"name":"2030","year":2030,"length_years":10}],   // with years: investment periods (PyPSA)
    "discount_rate"?: 0.07,                                        // global; overrides per-tech interest_rate

    "carriers":  { "<id>": { "unit"?: "MWh", "co2_intensity"?: 0.2, "color"?: "#…", "native"?: {…} } },
    "nodes":     { "<id>": { "coords"?: {"lat":48.8,"lon":12.9,"alt"?:0},
                             "allowed_techs"?: ["…"], "available_area"?: 1e4,
                             "climate"?: {"ghi":"series_id","temp_air":15,…},  // AdOpT weather for physics techs
                             "electrical"?: {"v_nom":20, "shunt"?:{"g":0,"b":0.001}},   // kV + shunt (S); the node's AC bus (PyPSA power flow)
                             "native"?: {…} } },
    "timeseries"?:{ "<id>": { "source": "inline"|"file", "values"?: [ … ], "path"?: "…", "column"?: "…" } },  // file: accepted by the schema, rejected by validation until a target reads files

    "technologies": {
      "<id>": {
        "role": "supply"|"demand"|"conversion"|"storage",
        "node": "<id>" | ["<id>", …],
        "carrier_in"?:  "<id>" | ["<id>"],
        "carrier_out"?: "<id>" | ["<id>"],
        "efficiency"?:  Value,
        "flows"?: [ { "carrier":"<id>", "direction":"in"|"out", "ratio":0.4, "reference"?:true } ],
        "performance"?: {
          "type": "constant"|"piecewise"|"physics",
          "efficiency"?: Value,                                  // constant
          "breakpoints"?: [ {"load":0.3,"efficiency":0.40} ], "min_load"?: 0.3,  // piecewise
          "model"?: "pv"|"wind"|"heat_pump"|"open_hydro",        // physics
          "params"?: { … }, "precomputed"?: "series_id"
        },
        "capacity"?:  { "existing":0, "expandable":true, "min"?:0, "max"?:500, "unit"?:"MW",
                        "per_unit"?:10, "units_min"?:0, "units_max"?:12, "systemwide_min"?:0, "systemwide_max"?:1000,
                        "decommission"?: { "mode":"impossible"|"continuous"|"only_complete", "cost"?:0 } },
        "operation"?: { "max_pu"?:Value, "min_pu"?:Value, "ramp_up"?:0.6, "ramp_down"?:0.6,
                        "committable"?:true, "start_up_cost"?:5000, "shut_down_cost"?:0,
                        "min_uptime"?:3, "min_downtime"?:2,
                        "ramp_start_up"?:0.6, "ramp_shut_down"?:0.6, "standby_cost"?:5, "initial_uptime"?:2 },  // UC extras (PyPSA)
        "storage"?:   { "energy_capacity"?:{…Capacity}, "max_hours"?:4, "charge_eff"?:0.95,
                        "discharge_eff"?:0.95, "self_discharge"?:0.001, "inflow"?:Value,
                        "spill_cost"?:0, "initial_soc"?:0.5, "cyclic"?:true,
                        "max_charge_rate"?:0.5, "max_discharge_rate"?:0.5, "depth_of_discharge"?:0.1,
                        "no_simultaneous_charge_discharge"?:false,
                        "soc_profile"?:Value },                                 // pinned state of charge (PyPSA StorageUnit)
        "demand_profile"?: Value,                                // role: demand
        "dispatch"?: Value,                                      // fixed MW per timestep; power_flow input (ignored elsewhere)
        "electrical"?: { "reactive_dispatch"?:Value, "power_factor"?:0.95, "leading"?:false,
                         "control"?:"pq"|"pv", "voltage_setpoint"?:1.0 },     // reactive power + voltage control (power_flow)
        "area"?:   { "max"?:1500, "per_capacity"?:0.01 },        // Calliope-only
        "source"?: { "cap"?:…, "unit"?:"per_area", "max"?:Value },// Calliope-only
        "costs"?:  { "<class>": { "investment_per_capacity"?:6e5, "investment_per_energy_capacity"?:…,
                                  "fixed_om"?:…, "variable_om"?:Value, "variable_om_quadratic"?:0.01, "purchase"?:… } },
        "lifetime"?: 25, "interest_rate"?: 0.07, "cost_basis"?: "overnight"|"annualized",
        "node_overrides"?: { "<node>": { …subset of the fields above… } },
        "native"?: { "pypsa"?:{…}, "calliope"?:{…}, "adopt-net0"?:{…} }
      }
    },

    "transmission"?: { "<id>": { "carrier":"<id>", "from":"<node>", "to":"<node>",
                                 "bidirectional"?:true, "capacity"?:{…}, "efficiency"?:0.97,
                                 "distance"?:…, "costs"?:{…},
                                 "electrical"?: { "kind":"line"|"transformer", "r":1.2, "x":3.5,   // AC branch (PyPSA power flow)
                                                  "r_per_length"?:0.12, "x_per_length"?:0.35,       // … or per km × distance
                                                  "s_nom"?:20, "num_parallel"?:1, "b"?:0.0001, "g"?:0,
                                                  "tap_ratio"?:1, "phase_shift"?:0, "max_loading"?:0.9 },
                                 "dispatch"?: Value,                                   // fixed transfer of a controlled link (HVDC)
                                 "lifetime"?:40, "interest_rate"?:0.05, "cost_basis"?:"overnight",  // annualize the arc's investment
                                 "native"?:{…} } },
    "trade"?: { "<id>": { "node":"<node>", "carrier":"<id>",
                          "import"?:{ "limit"?:500, "price"?:Value, "emission_factor"?:0.35 },
                          "export"?:{ "limit"?:500, "price"?:Value } } },
    "emission_limits"?: [ { "name"?:"co2_cap", "carrier"?:"co2", "sense":"<="|">="|"==",
                            "limit":1e6, "period"?:"…" } ],
    "constraints"?: [ { "name":"…", "sense":"<="|">="|"==", "bound":800, "period"?:"…",
                        "terms":[ { "coefficient":1,
                                    "variable":"capacity"|"flow_out"|"flow_in"|"storage_cap"|"emissions",
                                    "techs"?:["…"], "carriers"?:["…"], "nodes"?:["…"] } ] } ],
    "native"?: { "pypsa"?:{…}, "calliope"?:{…}, "adopt-net0"?:{…} }
  },

  "experiment": {
    "mode"?: "plan"|"operate"|"alternatives"|"pareto"|"monte_carlo"|"stochastic"|"power_flow",
    "power_flow"?: { "method"?:"newton_raphson"|"linear", "slack":"<tech>"|"<tech>@<node>",
                     "distribute_slack"?:false, "tolerance"?:1e-6 },   // mode power_flow (PyPSA)
    "network"?: { "model"?:"transport"|"linear_power_flow", "security"?:{ "outages":["<arc>"] } },  // the voltage law inside optimize (PyPSA)
    "objective"?: "min_cost"|"min_emissions",
    "foresight"?: "perfect"|"myopic",
    "time_aggregation"?: { "method":"typical_days"|"resample", "periods"?:12, "resolution"?:"3H" },
    "operate"?: { "window":"24H", "horizon":"48H" },                                   // rolling horizon
    "alternatives"?: { "number":5, "slack"?:0.1, "scoring_algorithm"?:"integer"|"relative_deployment" },  // MGA / SPORES
    "pareto"?: { "points":6 },  "monte_carlo"?: { "samples":25, "standard_deviation"?:0.1, "on"?:["technology_capex"] },
    "allow_unmet_demand"?: { "enabled":true, "penalty_price"?:1000 },  "copperplate"?: false,
    "emission_accounting"?: "net"|"positive_only",
    "reporting"?: { "output_dir"?:"…", "case_name"?:"…", "save_logs"?:"…", "shadow_prices"?:true },
    "scenarios"?: [ { "name":"…", "weight"?:0.5, "overrides"?:{ "<path>": 1.2 } } ],
    "sweep"?: [ { "parameter":"technologies.pv.capacity.max", "values":[200,400] } ],
    "solver": { "name":"highs", "options"?:{ … } },
    "custom_math"?: true,        // Calliope: render constraints as add_math (default true)
    "native"?: { … }
  }
}
```

**Model invariants:** node and carrier are orthogonal (PyPSA's bus is derived);
`flows` supersedes `efficiency` for multi-carrier conversion; anything a target
can't represent portably goes in a `native` block (which makes that payload
target-locked).

### Runtime / API schemas

```jsonc
// GET /jobs/{id}/status  (JobView). ?target= on validate/convert/simulate
// accepts one target, a comma-separated list, or "all" — a multi-target job
// runs every framework in turn ("target":"pypsa,calliope,adopt-net0"). "all"
// means the default targets; a version variant ("calliope-0.6") runs only
// when named, and a version alias ("calliope-0.7") resolves to its default.
// With API_KEY configured, validate/convert/simulate payloads must carry a
// matching top-level "api_key" or the request is a 401 {"error":…}.
{ "id":"…", "target":"pypsa", "state":"queued|running|succeeded|failed",
  "warnings":[…], "error":"", "log":"…full console log…",
  "runs":[ RunResult ], "created":"…","started":"…","finished":"…" }

// one run (RunResult; one per target x sweep point)
{ "target":"pypsa", "index":0, "input_dir":"…/run_0/input",
  "plan":{ "target":"pypsa","command":["python","run.py"],"work_dir":"…" },
  "assigned":{ "technologies.pv.capacity.max":400 },
  "stdout":"…", "exit_code":0, "error":"" }

// POST /simulate (202)
{ "scheduled":true, "id":"…", "state":"queued", "target":"pypsa",
  "warnings":[…], "status_url":"/jobs/…/status", "result_url":"/jobs/…" }

// GET /capabilities
{ "targets":["adopt-net0","calliope","calliope-0.6","pypsa"],   // every selectable target
  "default_targets":["adopt-net0","calliope","pypsa"],           // what ?target=all runs
  "aliases":{ "calliope-0.7":"calliope" },                        // version-explicit spellings
  "versions":{ "calliope":"0.7.0", "calliope-0.6":"0.6.8", "pypsa":"1.2.4", "adopt-net0":"0.1.10" },
  "features":{ "pypsa":{ "mode:stochastic":true, … }, "calliope":{…}, "calliope-0.6":{…}, "adopt-net0":{…} } }
```

### Result bundle (`GET /jobs/{id}` → zip)

```
<id>.zip
├── metadata.json      # JobView without the log (state, warnings, error, runs, timings)
├── log.txt            # the whole console log
├── results.json       # the RunResult array
├── config.json        # the initial request payload (byte-identical, minus any api_key)
└── files/             # the complete emitted tree, per run
    ├── config.json    # (same file, on-disk copy in the job dir)
    └── run_0/
        ├── run.py     # the executed Python driver (PyPSA/AdOpT; Calliope uses its CLI)
        ├── input/…    # CSVs / model.yaml / input_data + sidecars
        └── output/…   # solver results: network.nc (PyPSA; + network_baseline.nc for MGA),
                       #   results.nc + csv/ (Calliope);
                       #   AdOpT writes results/ (H5 + Summary.xlsx) under input/
```

A **multi-target** job (`?target=pypsa,calliope` or `target=all`) nests each
framework's tree one level deeper — `files/<target>/run_0/…` — so one zip
carries every requested model, its results, the shared initial config, and the
combined log.

### Emitter outputs

| Target | Entrypoint | Contents |
|---|---|---|
| PyPSA | CSV folder | `network.csv` (name + pinned `pypsa_version`), `buses.csv`, `carriers.csv`, `generators.csv`, `loads.csv`, `links.csv`, `storage_units.csv`, `global_constraints.csv` (unscoped constraints only), `snapshots.csv` (plain, or `(period, timestep)` under investment periods), materialized time-varying CSVs (`loads-p_set`, `generators-p_max_pu`/`p_min_pu`, `generators-marginal_cost`, `links-marginal_cost`, `storage_units-inflow`, `storage_units-state_of_charge_set`; `*-p_set`/`*-q_set` dispatch series in mode power_flow), `_constraints.json` (scoped constraints, applied by run.py), `_scenarios.json` (stochastic mode), `_performance.json`, `_native.pypsa.json`; mode power_flow or network model linear_power_flow: `lines.csv`, `transformers.csv`, `v_nom` + carrier `AC` on the electrical buses (power flow: `control`/`p_set`/`q_set`, `shunt_impedances.csv`, dispatch series); energy-sized storage: `stores.csv` + charger/discharger links + `_storage.json`; investment periods: `investment_periods.csv` and `(period, timestep)` snapshots |
| Calliope 0.7 (`calliope`) | `model.yaml` | `config` (`init.mode`/`init.resample`/`solve.solver`/`solve.spores`/`init.math_paths`+`extra_math` incl. `milp`), `techs` (incl. co2 `cost_flow_in` / `cost_source_use` on supply, trade market techs, MILP unit-commitment params, piecewise breakpoint params), `nodes`, `data_tables` (`table` + `add_dims.inputs`) + materialized series CSVs (demand, availability, source), `data_definitions.objective_cost_weights`, `additional_math.yaml` (constraint IR + flow-ratio pinning + piecewise machinery), `_constraints.json` sidecar when `custom_math=false` |
| Calliope 0.6 (`calliope-0.6`) | `model.yaml` | `model` (`calliope_version`, `timeseries_data_path`, `subset_time`, `time` resample / k-means `apply_clustering`), `run` (`mode` plan/operate/spores, `solver`, `objective_options.cost_class` weights, `cyclic_storage`, `operation`, `spores_options`), `techs` (`essentials`/`constraints`/`costs`; `conversion_plus` carrier tiers + `carrier_ratios`; trade market techs; `units_max`/`energy_cap_per_unit` MILP; `emissions` cost class), `locations` (coordinates, `available_area`, per-location `file=` resources + overrides), `links` (`from,to` → transmission techs + distance), `group_constraints` (constraint IR, emission limits, annual output bounds, SPORES cost slack), `timeseries_data/*.csv` (datetime index, one column per node; demand negative) |
| AdOpT-NET0 | `input_data/` | `Topology.json`, `ConfigModel.json` (glpk, `write_results: 1`), `NodeLocations.csv`, `_meme_overrides.json` (keyed node → tech), per-period `Networks.json`, per-node `Technologies.json` (database tech names) + `ClimateData.csv` + `CarbonCost.csv` + `carrier_data/*.csv` (`;`-delimited; demand/import/export incl. per-timestep prices) + `EnergybalanceOptions.json`. The run step copies tech data + applies overrides. |

The orchestrator writes each run's Python driver as `run_i/run.py` (PyPSA:
import → sidecar constraints → mode branch (plan / rolling-horizon operate /
MGA / stochastic / power flow: `lpf()` then `pf()` with the result tables under
`output/power_flow/`) → export `network.nc`, non-zero exit unless optimal or
converged; AdOpT:
copy tech data → apply node-keyed overrides → `quick_solve`). Calliope runs via
its CLI with `--save_netcdf`/`--save_csv` — `calliope` for 0.7, `calliope-0.6`
for 0.6 (two venvs in the image). The executed script ships in the result
bundle.


