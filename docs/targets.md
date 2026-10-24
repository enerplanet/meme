# Targets and versions

A **target** is one framework backend: its capability profile, validation
gates, emitter and run plan live together in one package under
`internal/target/` and self-register with the target registry. The service
ships four:

| Target | Framework | Version | Selected by |
|---|---|---|---|
| `pypsa` | [PyPSA](https://pypsa.org) | 1.2.4 | default (`all`) |
| `calliope` | [Calliope](https://www.callio.pe) 0.7 | 0.7.0 | default (`all`); alias `calliope-0.7` |
| `calliope-0.6` | Calliope 0.6 | 0.6.8 | by name only (version variant) |
| `adopt-net0` | [AdOpT-NET0](https://github.com/UU-ER/AdOpT-NET0) | 0.1.10 | default (`all`) |

## Framework versions are targets

A framework may be registered once per supported release. The bare name is
the default version; another release is a **version variant**
(`calliope-0.6`) that runs only when named, so `?target=all` expands to the
default targets and never runs two releases of one framework unless asked.
A version-explicit **alias** of a default (`calliope-0.7` → `calliope`) keeps
a pinned spelling valid when the default moves on and resolves everywhere a
target name is accepted. `GET /capabilities` reports `targets` (every
selectable name), `default_targets` (what `all` runs), `aliases`, `versions`
(the framework release each target drives) and the feature matrix.

Each version has its own emitter, capability profile and native key:
`native.calliope` is Calliope 0.7 syntax and is never applied by the 0.6
target, which reads `native["calliope-0.6"]`. The decision and its
alternatives are recorded in
[ADR-0006](https://github.com/enerplanet/meme/blob/main/decisions/0006-framework-versions-as-targets.md).

```bash
curl -s -X POST 'localhost:8080/validate?target=calliope-0.6' -d @examples/shared_full.json
curl -s -X POST 'localhost:8080/convert?target=calliope,calliope-0.6' -d @examples/shared_full.json
```

The pinned releases are installed by the [environment image](https://github.com/enerplanet/meme/blob/main/operations/environment.md);
the [capability reference](https://github.com/enerplanet/meme/blob/main/reference/capability-reference.md) lists, property
by property, what each target honors.

## Extension points and known caveats

- **All emitters produce solver-runnable output** for a faithful core; the
  long tail routes through `native` and sidecars. Verified end-to-end against
  pinned framework versions (see the [environment](operations/environment.md)): PyPSA (HiGHS), Calliope 0.7.0
  and 0.6.8 (cbc, incl. plan/operate/SPORES/emission-limits), AdOpT-NET0.
- **Calliope versions are targets.** `calliope` is Calliope 0.7.0 (alias
  `calliope-0.7`); `calliope-0.6` is Calliope 0.6.8 — a separate emitter for
  the legacy dialect, driven by the `calliope-0.6` CLI from a second venv in
  the image. `?target=all` runs the default `calliope` only; name
  `calliope-0.6` to run it (alone or alongside). The two targets have their
  own capability profiles and native keys (`native.calliope` is 0.7 syntax,
  `native["calliope-0.6"]` is 0.6 syntax), and `GET /capabilities` reports
  `default_targets`, `aliases` and `versions`.
- **Calliope 0.7 specifics** (all verified against `calliope==0.7.0`):
  - Run mode and solver names are translated to Calliope's own (`plan→base`,
    `alternatives→spores`; `highs→cbc`). `experiment.custom_math` renders the
    linear-constraint IR to `additional_math.yaml`, registered via
    `config.init.math_paths` + `extra_math`.
  - 0.7.0 (vs. the earlier dev7 pin): data tables are spelled `table` /
    `add_dims.inputs`; a supply tech's fuel cost is `cost_source_use`
    (`cost_flow_in` no longer applies to supply); the `defined()` math helper
    is gone, so ratio-pinning math iterates techs and subsets with
    `[tech] in techs`; node coordinates must be given on all nodes or none
    (a partial set is dropped with a warning); an already-annualized cost
    basis pins `cost_depreciation_rate: 1`.
  - Emissions: `co2_intensity` on a consumed carrier becomes a per-tech
    `cost_flow_in[costs=co2]`, with `objective_cost_weights[co2]=0` so it is
    tracked (for `emission_limits`) but not minimized. **Limitation:** this uses
    costs-only indexing, exact for a single fuel per tech (the common case);
    a tech burning two different co2 carriers would need per-carrier indexing
    (which clashes with Calliope's built-in cost expression).
  - Inline time series are materialized to data-table CSVs at emit time.
  - Operate mode sizes nothing: Calliope reads `flow_cap`, `storage_cap`,
    `source_cap` and `area_use` as inputs (default infinite) and ignores
    `*_max` bounds. The emitter installs every element at its fixed size —
    techs and lines at `capacity.existing`, else `capacity.max` (warned),
    imports at their `limit`, `source.cap`/`area.max` as `source_cap`/
    `area_use` — and validation rejects a model with no fixed capacity at
    all, which Calliope would otherwise solve and then abort on (#8).
- **Calliope 0.6 specifics** (all verified against `calliope==0.6.8`):
  - The whole model is one document: `model` (incl. `calliope_version`,
    `timeseries_data_path`), `run`, `techs`, `locations`, `links`,
    `group_constraints`. Time series are `file=<file>.csv:<node>` references
    into `timeseries_data/` (demand negative, one column per node).
  - 0.6 requires `lifetime` + `interest_rate` wherever a fixed cost appears;
    missing values (or an annualized basis) become lifetime 1 / rate 0, i.e.
    the figure is charged in full each year — the same outcome as 0.7's
    default depreciation rate.
  - Storage has one `energy_eff` for both directions (asymmetric
    charge/discharge efficiencies emit their geometric mean, with a warning)
    and a model-wide `run.cyclic_storage` (mixed per-tech settings warn and
    disable it); operate mode is never cyclic.
  - Gains over 0.7: built-in k-means typical days (`time_aggregation:
    typical_days`, `periods` = k, fixed `random_seed`) and a native
    emissions-minimizing objective. Absent: indexed parameters, piecewise
    performance, custom math (constraints are single-term
    `group_constraints`), multi-input `flows`, `time.weights`, `shadow_prices`.
- **HiGHS + Calliope is unsupported** — Calliope's pyomo backend cannot drive
  HiGHS (ASL needs a nonexistent binary; the persistent/appsi paths hit an
  `add_block`/`solver_io` bug), and 0.6.8's pyomo 6.2 predates the HiGHS
  interface entirely. Use `cbc` for both Calliope targets (free/OSS); HiGHS
  remains the solver for PyPSA. (0.7.0 ships a direct `config.build.backend:
  highs` without piecewise support; not wired yet.)
- **Runner commands** assume the Python tools are on PATH; `DryRunner` is the
  default so the service runs with no Python installed (`-exec` switches to
  `CommandRunner`). The `environment/` image provides all three frameworks
  (both Calliope releases) + cbc.
- **JobStore is in-memory** — jobs don't survive a restart. Finished jobs are
  GC'd after 24 h (15-min sweep, see `cmd/meme/main.go`); a persistent store is
  the production follow-up.
- **AdOpT-NET0 runs and solves** (verified end-to-end via `/simulate -exec`), but
  it works differently from PyPSA/Calliope in two ways you must know:
  - **Solver: `cbc`/`highs` are not options.** `adopt_net0` supports only
    **gurobi** and **glpk**; the emitter selects **glpk** (free/OSS) regardless
    of `experiment.solver.name`.
  - **Run modes are ConfigModel toggles, not separate entrypoints.** AdOpT's
    single `quick_solve()` branches on `ConfigModel.optimization`, so the emitter
    translates `experiment.mode` there rather than into the run command:
    `pareto` sets `objective: pareto`, `monte_carlo` sets `monte_carlo.N > 0`
    (default 10), and `time_aggregation.method: typical_days` sets
    `typicaldays.N` to the requested `periods`. Without this the capability
    matrix would accept the payload but adopt would silently solve a plain
    cost-optimal plan. Iteration counts are tunable via
    `experiment.solver.options` — `{"monte_carlo": {"N": 25}}` and
    `{"pareto": {"points": 6}}` — mirroring Calliope's `options.spores`.
  - **Technologies are database-driven.** A tech is a *named* library entry
    (`Photovoltaic`, `Storage_Battery`, `HeatPump_AirSourced`, …). The emitter
    maps supported canonical techs to a database name, lists it in
    `Technologies.json`, and writes an economics/size override sidecar
    (`_meme_overrides.json`); the run step copies the tech data via
    `copy_technology_data` and patches it with the overrides before solving.
    **Only mapped roles are emitted** (storage → `Storage_Battery`; physics
    supply → `Photovoltaic`/`WindTurbine_Onshore_1500`/`HeatPump_AirSourced`).
    Generic supply/conversion techs (`ccgt`, `chp`) have no library counterpart
    and are **skipped** — supply them via `native.adopt-net0` (name a real
    database tech in `Technologies.json` + override).
  - **Physics techs need climate data — supply it via `node.climate`.**
    `Photovoltaic`/wind read `ClimateData.csv` (ghi/dni/dhi/temp_air/rh/ws10/
    hydro_inflow). The emitter materializes those columns from a node's
    `climate` map (each entry a scalar or timeseries id), so a `/simulate`
    payload can carry the weather and PV/wind produce real output. Columns left
    unspecified are blank. Demand, trade (import/export), emission limits, node
    locations, and storage all work from the canonical model directly.
- **Adding/altering a target** is largely a data edit to the capability matrix
  plus one `Emitter` implementation; the schema, validation, and orchestrator are
  target-agnostic. **Adding a framework version** is the same work under a
  `-<major.minor>` name: register it (as a `target.Variant` of the default if
  it must not join `all`), give it its own native key, pin it in its own
  venv in `environment/`, and run the corpus against it.


