# Unified schema vs. the meme Go implementation

Comparison of [`unified_schema.json`](unified_schema.json) (revision 1, the framework-driven
unification of AdOpT-NET0 0.1.10 / PyPSA 1.2.4 / Calliope 0.7.0.dev7) against meme's canonical
Go model (`internal/model`), and the decision log for
[`revised_unified_schema.json`](revised_unified_schema.json) (revision 2), which converges the two.

**Decision rule** (applied per divergence): if the Go implementation's design is better, adopt the
implemented way; if the schema's design is better or strictly richer, keep the schema's way and mark
it as a proposed Go extension. In revision 2 every property carries an `x-go` annotation naming the
Go field it maps to — or `null`, meaning *proposed extension, not yet in Go*.

Revision 2 is verified against reality: all five real payloads in `examples/` (`payload_full.json`,
`shared_full.json`, `adopt_full.json`, `calliope_full.json`, `pypsa_full.json`) validate against it,
and eleven negative tests (mirroring `Model.Validate` rules) are rejected.

---

## 1. Architecture

| Aspect | Rev-1 schema | Go implementation | Decision (rev 2) |
|---|---|---|---|
| Document shape | One flat document mixing physical system, run spec and reporting | `Job = Model + Experiment`: physical system strictly separated from *how to solve* ([experiment.go](../internal/model/experiment.go)) | **Go.** The separation is the better factoring (a model can be re-run under many experiments) and makes the schema validate meme REST payloads directly. Rev 2 is a Job schema: `{api_key?, model, experiment?}`. |
| Value type | `ScalarOrSeries`: number \| inline array \| `ts:` ref \| non-finite literal | `Value`: number \| `ts:` ref \| indexed `{data, index, dims}` ([types.go](../internal/model/types.go)) | **Union of both.** Indexed params (Go) are essential for Calliope cost classes; inline arrays and `inf` literals (schema) are kept as extensions. |
| Native passthrough | Component blocks free-form; document-level blocks `$ref`-validated against the three source schemas | `Native{pypsa, calliope, adopt-net0}` as opaque `json.RawMessage` everywhere | **Go.** Real payloads carry annotation keys (e.g. `native.pypsa.note`) that strict `$ref` validation rejects, and native is *by design* an unvalidated escape hatch. Rev 2 makes all native blocks free-form and cites the source schemas as the recommended vocabulary. |
| Capability gating | `x-map: null` prose annotations | Machine-checked `Feature` matrix + `ValidateFor(target)` ([capability.go](../internal/model/capability.go)) | **Go** enforces; the schema documents. The `x-go`/`x-map` annotations are the documentation layer over the same knowledge. |
| Framework mapping docs | `x-map` per property | Comments in emitters | **Schema.** `x-map` annotations are carried into rev 2. |

## 2. Decision log by area

### 2.1 Naming — Go wins wholesale

Rev 2 adopts Go's JSON field names so it validates actual payloads. Renames from rev 1:

| Rev 1 | Rev 2 (= Go) |
|---|---|
| top-level `model` (metadata block) | `model.metadata` |
| `links` | `transmission` |
| `markets` / `MarketSide` | `trade` / `TradeSide` |
| `technologies.<id>.type` | `role` |
| `nodes` placement (list-or-map on the tech) | `node` (string-or-list) + `node_overrides` (typed map) |
| `latitude`/`longitude`/`altitude` on the node | `coords.{lat, lon, alt}` |
| `constraints[].limit` | `bound` |
| `length` | `distance` |
| `availability.max/min` | `operation.max_pu` / `operation.min_pu` |
| `ramping.up/down` | `operation.ramp_up` / `ramp_down` |
| `unit_commitment.enabled/…` | `operation.committable`, `start_up_cost`, `shut_down_cost`, `min_uptime`, `min_downtime` |
| `storage.hours` | `storage.max_hours` |
| `storage.charge_efficiency`/`discharge_efficiency` | `charge_eff` / `discharge_eff` |
| `storage.standing_loss` / `initial_state` | `self_discharge` / `initial_soc` |
| `capacity.modular.unit_size` | `capacity.per_unit` |
| `costs.discount_rate` (per component) | `interest_rate` (tech level) |
| `costs.investment_fixed` | `costs.<class>.purchase` |
| term variable `storage_energy_capacity` | `storage_cap` |
| mode `spores` | `alternatives` (framework-neutral; the Calliope emitter translates to `spores`) |
| `demand.profile` | `demand_profile` |

### 2.2 Concepts where Go was better — adopted

| Concept | Go design adopted | Why it beats rev 1 |
|---|---|---|
| Multi-carrier conversion | `flows: [{carrier, direction, ratio, reference}]` | Rev 1's `carrier_ratios {carrier: number}` relied on a sign/placement convention; Go's explicit direction + single reference-flow marking is unambiguous. Rules mirrored into JSON Schema: ≥ 1 input flow, ≤ 1 reference flow, non-reference flows must state `ratio` (the reference flow is its own basis). |
| Performance beyond a scalar | `performance {type: constant \| piecewise \| physics, breakpoints, min_load, model, params, precomputed}` | Rev 1 relegated part-load curves and physics models (pvlib PV, wind curve, heat-pump COP, open hydro) to `native.adopt-net0`, losing portability. Go's IR is target-neutral with a `precomputed` profile fallback for PyPSA/Calliope. Validation mirrored: ≥ 2 breakpoints, loads in [0,1], efficiency > 0, physics model ∈ {pv, wind, heat_pump, open_hydro}. |
| Cost classes | `costs: map[class]CostClass`, `"monetary"` = the class every emitter reads | Rev 1 had a single monetary `CostSpec` plus a dangling `economics.cost_classes` declaration. Go's map is the real multi-class mechanism (e.g. a `co2` class) with no redundant declaration. |
| Cost basis | `cost_basis: overnight \| annualized` enum + one investment field | Simpler than rev 1's two mutually-exclusive investment fields; applies uniformly per technology. Cross-field rule (overnight ⇒ needs `lifetime` + `interest_rate`) stays Go-enforced. |
| Storage energy CAPEX | `investment_per_energy_capacity` | Fixes a genuine rev-1 defect: one `CostSpec` could not carry power CAPEX (€/MW) and energy CAPEX (€/MWh) simultaneously. |
| Storage spill | `spill_cost` | Missing from rev 1 (PyPSA `spill_cost`). |
| Constraint IR | `emission_limits` (typed common case) + `constraints` (generic linear terms), variable enum incl. **`emissions`** | Rev 1's typed shortcut union (`capacity_expansion_limit`, …) is expressible as terms over the `capacity` variable; the `emissions` variable existed only in Go. Length-weighted transmission-volume limits remain inexpressible in both → native. |
| Run modes | six-value enum `plan / operate / alternatives / pareto / monte_carlo / stochastic` | Rev 1 conflated pareto with the objective and stochastic with an `uncertainty.stochastic` flag; Go's flat mode enum is the cleaner factoring. Objective collapses to `min_cost \| min_emissions`; "cost under a cap" = `min_cost` + an `emission_limits` entry. |
| Scenario/sweep addressing | dotted parameter paths, shared between `scenarios[].overrides` and `sweep[].parameter` | One addressing scheme for both mechanisms instead of rev 1's nested partial documents. Extension kept: override *values* may be string/boolean (Go: numbers only). |
| Node model | `coords`, `allowed_techs`, `climate` (weather series feeding physics performance) | Rev 1 had none of `allowed_techs`/`climate`. Rev 1's per-node `carriers` restriction was **dropped**: carrier presence is derivable from placed technologies/transmission/trade, and Go keeps it non-redundant on purpose. |
| Metadata | `currency_year` | Real vs. nominal cost figures; missing from rev 1. |
| Time subset | `time.subset [start, end]` | Missing from rev 1. |
| Validation split | Structural rules in JSON Schema; referential integrity (nodes/carriers/series/override keys resolve) in `Model.Validate` | JSON Schema cannot express cross-collection references; the schema documents which rules are Go-enforced. |

### 2.3 Concepts where the rev-1 schema was better — kept as extensions (`x-go: null`)

Every entry below is marked `EXTENSION` in rev 2 and is a concrete, prioritized proposal for the Go model.

| Extension | Where | Today's Go workaround | Value |
|---|---|---|---|
| Structured per-mode options: `operate {window, horizon}`, `alternatives {number, slack, scoring_algorithm}`, `pareto {points}`, `monte_carlo {samples, standard_deviation, on}` | `experiment.*` | Tunnelled through `solver.options` namespaces (`solver.options.spores`, `.operate`, `.monte_carlo`, `.pareto`) and parsed by emitters — solver options carrying non-solver concerns is a smell the emitters themselves note | **High** — typed, validated, discoverable; removes emitter-side option parsing |
| First-class `solver.time_limit / mip_gap / threads` | `experiment.solver` | free-form `options` | High — the three options every run tunes, with per-target unit conversion documented (AdOpT hours) |
| `allow_unmet_demand {enabled, penalty_price}` and `copperplate` | `experiment` | AdOpT emitter hardcodes `violation = -1`, `copperplate = 0` | High — feasibility debugging is a standard workflow (Calliope `ensure_feasibility`, AdOpT `violation`) |
| `reporting {output_dir, case_name, save_logs, shadow_prices}` | `experiment` | service-layer conventions, emitter-hardcoded paths | Medium |
| Capacity: `systemwide_min/max`, `units_min/max`, `decommission {mode, cost}` | `capacity` | native blocks | Medium — all three frameworks can honor at least part of each |
| Storage: `max_charge_rate`, `max_discharge_rate`, `depth_of_discharge`, `no_simultaneous_charge_discharge` | `storage` | native blocks; `max_hours` covers only the fixed-ratio case | Medium — completes the flexible power/energy sizing story (PyPSA Store+links, AdOpT flexratio, Calliope `flow_cap_per_storage_cap_*`) |
| Transmission: `loss_per_distance`, `min_flow`, `emission_factor`, `energy_consumption {carrier, per_flow, per_flow_distance}`, `active`; CostClass: `investment_per_capacity_distance` | `transmission`, `costs` | native blocks | Medium — first-class in AdOpT network files and Calliope per-distance parameters |
| Trade: series-valued `limit`, export-side `emission_factor`, `native` block | `trade` | scalar limit, import-only factor, no native | Medium |
| Operation: `equals_pu`, `max_startups`, `standby_power`; series-valued `ramp_up/ramp_down` | `operation` | native blocks; scalar ramps | Low–medium |
| Technology: `emission_factor`, `annual_output_min/max`, `build_year`, `active`, `demand_curtailable` | technology | carrier `co2_intensity` route only; omission instead of `active` | Low–medium |
| Time: explicit `timesteps`, `weights`; rich `periods` entries `{name, year, length_years, objective_weight}` | `model.time`, `model.periods` | plain period names | Medium — PyPSA multi-horizon needs the year; weights are how snapshot weighting is expressed |
| Model: global `discount_rate` | `model` | per-tech `interest_rate` only | Low (maps to AdOpT `global_discountrate`) |
| Experiment: `emission_accounting: net \| positive_only` | `experiment` | implicit (AdOpT `emissions_net` chosen by emitter) | Low |
| Time aggregation: method `cluster`, `cluster_series`, `keep_full_resolution_for` | `experiment.time_aggregation` | native | Low |
| Value: inline per-timestep arrays, non-finite literals (`inf`, `-inf`, `NaN` + YAML aliases) | `$defs/Value`, bounds | register a named series instead; omit unbounded fields | Low — mostly round-tripping convenience; Go's `Value.UnmarshalJSON` would need an array branch |
| Scenario override values beyond numbers | `experiment.scenarios` | `map[string]float64` | Low |

### 2.4 Corrections found by validating real payloads

Two rev-2 drafting errors were caught because `examples/*.json` are real Go-emitted payloads:

1. **`flows[].ratio` is not universally required.** The reference input flow omits `ratio` (it is its
   own basis, implicitly 1); only non-reference flows must state it. Encoded as a conditional
   requirement.
2. **Model-level native blocks must be free-form.** `examples/pypsa_full.json` carries
   `native.pypsa.note`; strict `$ref` validation against `pypsa_schema.json`
   (`additionalProperties: false` at its root) rejected it. Rev 2 follows Go's opaque-passthrough
   semantics and demotes the source schemas to recommended vocabulary.

## 3. Full mapping: Go type ↔ revision-2 schema

| Go (`internal/model`) | Rev-2 schema | Status |
|---|---|---|
| `Job{APIKey, Model, Experiment}` | root `{api_key, model, experiment}` | 1:1 |
| `Metadata{Name, Version, Description, Currency, CurrencyYear}` | `model.metadata` | 1:1 |
| `TimeConfig{Start, End, Resolution, Subset}` | `model.time` (+ ext. `timesteps`, `weights`) | 1:1 + ext |
| `Model.Periods []string` | `model.periods` (string or rich object) | superset |
| `Carrier{Name, Unit, CO2Intensity, Color, Native}` | `model.carriers.*` | 1:1 |
| `Node{Name, Coords, AllowedTechs, AvailableArea, Climate, Native}` | `model.nodes.*` | 1:1 |
| `TimeSeries{Source, Path, Column, Values}` | `model.timeseries.*` (+ ext. `unit`) | 1:1 + ext |
| `Technology{Role, Node, CarrierIn/Out, Efficiency, Flows, Performance, Capacity, Operation, Storage, DemandProfile, Area, Source, Costs, Lifetime, InterestRate, CostBasis, NodeOverrides, Native}` | `model.technologies.*` (+ ext. `emission_factor`, `annual_output_*`, `build_year`, `active`, `demand_curtailable`) | 1:1 + ext |
| `NodeOverride` | `node_overrides.*` | 1:1 |
| `Flow{Carrier, Direction, Ratio, Reference}` | `flows[]` | 1:1 (+ conditional ratio rule) |
| `Performance{Type, Efficiency, Breakpoints, MinLoad, Model, Params, Precomputed}` / `Breakpoint` | `performance` | 1:1 |
| `Capacity{Existing, Expandable, Min, Max, Unit, PerUnit}` | `capacity` (+ ext. `units_*`, `systemwide_*`, `decommission`) | 1:1 + ext |
| `Operation{MaxPU, MinPU, RampUp, RampDown, Committable, StartUpCost, ShutDownCost, MinUptime, MinDowntime}` | `operation` (+ ext. `equals_pu`, `max_startups`, `standby_power`) | 1:1 + ext |
| `Storage{EnergyCapacity, MaxHours, ChargeEff, DischargeEff, SelfDischarge, Inflow, SpillCost, InitialSOC, Cyclic}` | `storage` (+ ext. rate bounds, DoD, async) | 1:1 + ext |
| `AreaSpec{Max, PerCapacity}` / `SourceSpec{Cap, Unit, Max}` | `area` / `source` | 1:1 |
| `CostClass{InvestmentPerCapacity, InvestmentPerEnergyCapacity, FixedOM, VariableOM, Purchase}` | `costs.<class>` (+ ext. `fixed_om_fraction`, `fuel_cost`, `investment_per_capacity_distance`) | 1:1 + ext |
| `Transmission{Carrier, From, To, Bidirectional, Capacity, Efficiency, Distance, Costs, Native}` | `model.transmission.*` (+ ext.) | 1:1 + ext |
| `Trade{Node, Carrier, Import, Export}` / `TradeSide{Limit, Price, EmissionFactor}` | `model.trade.*` (+ ext.) | 1:1 + ext |
| `EmissionLimit{Name, Carrier, Sense, Limit, Period}` | `model.emission_limits[]` | 1:1 |
| `Constraint{Name, Terms, Sense, Bound, Period}` / `Term{Coefficient, Variable, Techs, Carriers, Nodes}` | `model.constraints[]` | 1:1 |
| `Native{PyPSA, Calliope, AdOpt}` | `native {pypsa, calliope, adopt-net0}` | 1:1 (free-form) |
| `Experiment{Mode, Objective, Foresight, TimeAggregation, Scenarios, Sweep, Solver, CustomMath, Native}` | `experiment` (+ ext. mode blocks, `emission_accounting`, `allow_unmet_demand`, `copperplate`, `reporting`) | 1:1 + ext |
| `Value` (scalar \| ref \| indexed) | `$defs/Value` (+ ext. inline array, non-finite) | superset |
| `StringList` | `$defs/StringList` | 1:1 |

Dropped from rev 1 (with rationale): the standalone `uncertainty` wrapper (subsumed by `mode` +
`scenarios` + `monte_carlo`), the 5-value objective enum (refactored into mode/constraints),
`economics.cost_classes` (redundant with cost-map keys), node `carriers` (derivable), typed
constraint shortcuts (expressible as terms), document-level `$ref`-validated native
(incompatible with real payloads and Go semantics).

## 4. Rules the schema cannot express (Go-enforced)

Documented in the relevant `description`s; enforced by `Model.Validate` / `Performance.Validate` /
`Value.StructuralError` / `NativeWarnings`:

- Referential integrity: every `node`, carrier, series reference, constraint scope entry and
  `node_overrides` key resolves to a defined entity (and override keys ⊆ the tech's `node` list).
- Overnight cost basis + `investment_per_capacity` ⇒ `lifetime` and `interest_rate` present.
- Piecewise breakpoints strictly increasing in load.
- IndexedParam shape: `len(data) == len(index)`, every index tuple has `len(dims)` coordinates.
- Native blocks target-lock the payload (warning, not error).
- Capability gating per target (`ValidateFor`): e.g. `min_emissions` objective is AdOpT-only,
  area/source constraints are Calliope-only, `monte_carlo`/`pareto` modes are AdOpT-only,
  `stochastic` is PyPSA-only.

## 5. Verification

Run against `docs/revised_unified_schema.json` (jsonschema, draft 2020-12):

- Meta-schema: valid.
- Positive: all five `examples/*.json` payloads validate unchanged.
- Negative (all correctly rejected): storage role without storage block; demand role without
  `demand_profile`; flows without an input; two reference flows; non-reference flow without
  `ratio`; constraint with empty `terms`; piecewise performance with one breakpoint; unknown
  physics model; both fixed-OM conventions at once; empty `carriers` map; mode `spores`
  (canonical name is `alternatives`).

## 6. Implementation status (2026-07-10)

A first convergence tranche has been implemented in the Go code; the affected `x-go: null`
annotations in `revised_unified_schema.json` were flipped to the real field paths:

- **Typed run spec** (`internal/model/experiment.go`): `Experiment.Operate/Alternatives/Pareto/MonteCarlo`
  and `Solver.TimeLimit/MIPGap/Threads` with range/enum validation (`Experiment.Validate`). Emitters read
  first-class fields with fallback to the legacy `solver.options` namespaces (first-class wins);
  `ValidateFor` warns on deprecated namespaces and on mode-mismatched blocks.
- **Validation hardening** (`internal/model/validate.go`): non-reference flows need a positive ratio and
  the reference must be an input; range checks on capacity/operation/storage/costs/transmission/trade;
  `from != to`; total-distance-loss sanity; climate-series references resolve.
- **Model extensions, wired through all three emitters** (`internal/model/model.go` + targets):
  `TradeSide.Limit` is now a `Value` (series limits: PyPSA fixed-`p_nom` generators with time-varying
  `p_max_pu`/negated `p_min_pu`, AdOpT per-timestep limit columns, Calliope rejects), export-side
  `TradeSide.EmissionFactor` (AdOpT column, Calliope co2 class, PyPSA warns), `Trade.Native`;
  `Storage.MaxChargeRate/MaxDischargeRate/DepthOfDischarge/NoSimultaneousChargeDischarge` (Calliope +
  AdOpT Flexibility/Performance patches; PyPSA rejects with pointers); `Transmission.LossPerDistance/MinFlow`
  (efficiency fold + `p_min_pu` for PyPSA, per-distance efficiency for Calliope).
- **Inline series** (`internal/model/types.go`, `intern.go`): `Value` accepts inline per-timestep arrays;
  `Model.InternInlineSeries` (called by `target.ValidateFor`) moves them into the `timeseries` registry
  under reserved `_inline:` ids, so emitters see exactly one series shape.
- The PyPSA driver (`internal/scripts/pypsa_run.py`) now forwards mapped solver options to linopy.

**Second tranche (2026-07-10, same day):** the remaining extensions are now implemented; the schema
carries exactly one deliberate `x-go: null` left ($defs/NonFiniteNumber — Go expresses "unbounded"
by omitting the field and does not parse the `"inf"` string literal; a parallel numeric type across
the model was judged not worth the API complexity).

- **Strictness fixes:** `metadata.name` required; `time.start`/`end` required and ISO-parseable
  (previously emitters silently fell back to a default horizon).
- **Run spec:** `emission_accounting` (AdOpT `emissions_pos`/`emissions_net`), `allow_unmet_demand`
  (AdOpT `violation` price; Calliope `ensure_feasibility` with a bigM-approximation warning),
  `copperplate` (AdOpT), `reporting` (AdOpT `case_name`; Calliope `save_logs`/`shadow_prices`;
  `output_dir` warned as service-managed), `time_aggregation` gains `cluster`/`cluster_series`/
  `keep_full_resolution_for` (the last wired to AdOpT `technologies_with_full_res`).
- **Model:** global `discount_rate` (AdOpT `global_discountrate`; overrides the per-tech
  `interest_rate` in the PyPSA annuity and Calliope `cost_interest_rate`), rich `periods`
  (string-or-object `Period` type; wire-compatible with the legacy `[]string`), `time.timesteps`
  (explicit labels; PyPSA/Calliope; rejected by AdOpT) and `time.weights` (PyPSA
  `snapshot_weightings`; Calliope scalar `timestep_weights`; rejected by AdOpT).
- **Capacity:** `units_min`/`units_max` (effective-bound helpers feed every emitter; Calliope
  `purchased_units_*` on committable techs), `systemwide_min`/`max` (Calliope
  `flow_cap_*_systemwide`; PyPSA synthesized sidecar constraints, gated out of operate/stochastic;
  rejected by AdOpT), `decommission{mode, cost}` (AdOpT native incl. `only_complete`; Calliope
  impossible/continuous; PyPSA impossible only).
- **Technology/operation:** `emission_factor` (AdOpT `Performance.emission_factor`; Calliope co2
  cost class; rejected by PyPSA), `annual_output_min/max` (PyPSA `e_sum_*` on supply),
  `build_year` (PyPSA; warned elsewhere), `active` (PyPSA/Calliope flags; AdOpT omission),
  `demand_curtailable` (Calliope `sink_use_max`), `equals_pu` (PyPSA pins both `p_*_pu`; Calliope
  `source_use_equals`; series via the materializer), `max_startups`/`standby_power` (AdOpT
  Performance patches).
- **Costs:** `fuel_cost` (Calliope `cost_flow_in`; PyPSA folds fuel/efficiency into
  `marginal_cost`; rejected by AdOpT), `fixed_om_fraction` (AdOpT's native `OPEX_fixed`
  convention; Calliope `cost_om_annual_investment_fraction`; PyPSA folds into `capital_cost`;
  mutually exclusive with `fixed_om`, overnight basis required),
  `investment_per_capacity_distance` (Calliope `cost_flow_cap_per_distance`).
- **Transmission:** `emission_factor` (Calliope co2 class), `energy_consumption` (PyPSA
  multi-port link withdrawing the aux carrier, one-way arcs only), `active`.
- **Type parity fixes:** `ramp_up`/`ramp_down` and transmission `efficiency` widened from
  `*float64` to `*Value` (the schema had declared them series-capable, so a schema-valid series
  would previously 400 at decode; now it decodes and is rejected with a scalar-only message);
  Calliope's flow costs merged into single multi-class `cost_flow_out`/`cost_flow_in` parameters;
  `Scenario.Overrides` widened to `map[string]any` with a numeric gate in the PyPSA stochastic
  translator.
- **Drift gate:** `make schema-check` (test/schema_check.py) validates the schema against the
  draft 2020-12 meta-schema and every payload in `examples/` plus the embedded scenario corpus
  (47 payloads) against it, so schema/code drift fails CI instead of accumulating.
- **Corpus, examples and container certification:** three extension scenarios
  (`pypsa_extensions`, `calliope_extensions`, `adopt_extensions`) exercise the new capabilities
  through the golden files, the validation matrix, and the full E2E sweep; the `examples/*_full.json`
  payloads were extended with the new fields their target honors, and a new full-tier test
  (`TestE2EExamplesSolve`) backs the README claim that every example runs to `succeeded` on real
  solvers. `make e2e-smoke` and `make e2e` pass in the container (HiGHS/CBC/GLPK), including the
  new scenarios and all five examples. Two fixes fell out of the certification: AdOpT
  `max_startups`/`standby_power` patches were inert until `ConfigModel.performance.dynamics` is
  enabled alongside them, and the multi-target E2E assertion compared the bundle's `config.json`
  against the raw payload without accounting for the documented `api_key` redaction.

## 7. File inventory

| File | Role |
|---|---|
| `docs/adopt_net0_schema.json`, `docs/pypsa_schema.json`, `docs/calliope_schema.json` | Untouched source-framework schemas (vocabulary for native blocks) |
| `docs/unified_schema.json` | Revision 1: framework-driven unification (kept for reference) |
| `docs/revised_unified_schema.json` | Revision 2: converged with `internal/model`; validates meme Job payloads; extensions marked `x-go: null` |
| `docs/unified_schema_vs_go.md` | This report |
