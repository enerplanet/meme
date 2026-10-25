# Changelog

All notable changes to MEME are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org), and git tags carry the `v` prefix
Go modules require (`v0.1.0-alpha`).

## [Unreleased]

### Added

- `docs/roadmap.md`: the coverage roadmap. It records the 2026-10-25
  measurement of what each framework accepts natively against what the
  emitters write for the whole scenario and example corpus, lists the
  emitter code paths no payload exercises, names two defects the capability
  reference hides (PyPSA storage units lose `capacity.max` and
  `operation.min_pu`/`max_pu`; AdOpT batteries ignore
  `storage.charge_eff`/`discharge_eff`), and plans the additive work for
  `0.2.x` and the breaking bundle for `0.3.0`, each with its deprecation.
  Technology catalogs become selectable: a `library` pin on a technology
  or arc names an upstream entry (AdOpT database, PyPSA standard types)
  or asks for a MEME-synthesized definition, and `experiment.catalog`
  sets the default.

## [0.2.0-alpha] - 2026-10-25

### Added

- Power flow as a PyPSA run mode ([ADR-0008]): `experiment.mode: power_flow`
  solves the AC network for a fixed dispatch with `lpf()` and `pf()` instead
  of optimizing. The canonical model describes the network itself —
  `node.electrical.v_nom`, `transmission.electrical` (`kind` line or
  transformer, `r`, `x`, `s_nom`, `num_parallel`, `tap_ratio`) and
  `technology.dispatch` (overridable per node) — and `experiment.power_flow`
  names the `slack`, the `method` (`newton_raphson` or `linear`),
  `distribute_slack` and the `tolerance`. The run writes `output/power_flow/`:
  `convergence.csv`, per-bus voltage magnitude, angle and power, per-line and
  per-transformer flows and loading, generator dispatch and a `summary.json`;
  a snapshot that does not converge fails the run. The API consumer chains a
  plan and a power flow as two calls; MEME holds no state between them.
- Gates around the new inputs: the mode routes through the `power_flow`
  capability (PyPSA only); power-flow inputs in an optimization mode (a
  `dispatch`, the `electrical` blocks) are kept and named in one warning
  while the arcs stay transport links (see *Changed*); in mode `power_flow`
  expandable capacities, trade,
  constraints, emission limits and scenarios are rejected, and
  `distribute_slack` requires a dispatch on the slack technology (PyPSA
  shares the balance in proportion to each generator's set-point, so a slack
  without one would receive none of it).
- Scenario `pypsa_power_flow`, the example payloads
  `examples/pypsa_power_flow*.json` (the reference network, the smallest
  valid power flow, a meshed ring with per-node dispatch, and the options:
  linear method, distributed slack, storage sign, a second carrier) and E2E
  checks for convergence, the linear method and a diverging load. The
  examples walkthrough documents the options, the result files and the
  rejected requests.

- Reactive power, voltage control, shunts and controlled transfers for the
  power flow ([ADR-0009]): `technology.electrical` with `reactive_dispatch`
  or `power_factor` (+ `leading`), `control: pv` and `voltage_setpoint`;
  `node.electrical.shunt`; `transmission.dispatch` as a controlled HVDC
  transfer on an arc without impedance; `b`, `g`, `phase_shift` and
  `r_per_length`/`x_per_length` on `transmission.electrical`. The result
  folder gains the reactive tables, the load and link tables and the shunt
  tables. Gates: one way to give Q, voltage set-points for `pv` generators
  and the slack only and agreeing per bus, an electricity arc needs an
  impedance or a transfer, every AC island needs a generator (an island
  without the named slack is reported as balanced by PyPSA), and the linear
  method says what it ignores. Scenario and example payload
  `pypsa_power_flow_reactive`, E2E-pinned on the held voltages.

- The electrical network inside the optimization ([ADR-0010]):
  `experiment.network.model: linear_power_flow` makes PyPSA solve under
  Kirchhoff's voltage law with Lines and Transformers (expansion from the
  arc's `capacity`, `electrical.max_loading` as `s_max_pu`), and
  `network.security.outages` runs the security-constrained solve; other
  targets reject the model like a mode. Transmission gains `lifetime`,
  `interest_rate` and `cost_basis`, and PyPSA prices the arc's investment
  and fixed O&M on Links and Lines. Every optimization run writes
  `output/results/` (dispatch, flows, storage states, marginal prices,
  `capacities.csv`, `statistics.csv`, `summary.json`).
- PyPSA optimization completeness ([ADR-0011]): an energy-sized storage
  (`storage.energy_capacity`) is a Store with charger and discharger Links,
  which honors independent rates, depth of discharge and the energy-capacity
  cost (`_storage.json` carries the equal-power and rate constraints);
  `storage.soc_profile` pins a StorageUnit's state of charge;
  `capacity.per_unit` builds integer modules; `decommission.mode:
  continuous` retires existing capacity at its fixed O&M and builds new
  capacity above it; `allow_unmet_demand` adds priced slack generators;
  `copperplate` joins every node through a free hub; `operation.ramp_start_up`,
  `ramp_shut_down`, `standby_cost`, `initial_uptime`, `initial_downtime` and
  `costs.<class>.variable_om_quadratic` map onto PyPSA's unit-commitment and
  cost attributes; `model.periods` with years are investment periods with
  `build_year`, `lifetime` and new builds per period. Scenarios and goldens
  for every claim, E2E on the real framework, and the example
  `examples/pypsa_planning.json`.

### Changed

- PyPSA no longer rejects independent storage rates, depth of discharge,
  continuous retirement, unmet demand or copperplate; the energy-capacity
  cost is priced instead of dropped. Calliope and AdOpT-NET0 keep every
  mapping they had; the new fields they have no slot for (`soc_profile`,
  the unit-commitment extras, the quadratic cost, investment periods) are
  ignored there with a warning. The validation matrix records every verdict
  that moved.
- `time.subset` and `costs.<class>.purchase` are rejected on PyPSA and
  AdOpT-NET0 instead of being dropped without a word: neither target has a
  slot (narrow `time.start`/`end`; fold the purchase cost into
  `investment_per_capacity`). Calliope 0.7 warns that `area.per_capacity`
  is not emitted (0.6 emits `resource_area_per_energy_cap`). The validation
  matrix records the moved verdicts.
- Power-flow inputs — `dispatch`, the `electrical` blocks on technologies,
  arcs and nodes — are ignored with one warning wherever no power flow runs:
  on Calliope and AdOpT-NET0 and in PyPSA's optimization modes alike. A
  `dispatch` in an optimization mode was rejected before; now one payload
  serves the planning call and the power-flow call ([ADR-0009]).
- The PyPSA driver parses its run configuration as JSON (`json.loads`)
  instead of pasting the JSON in as a Python literal, which broke on the
  first boolean setting.

### Fixed

- PyPSA `state_of_charge_initial` is absolute (MWh), but the canonical
  fraction `storage.initial_soc` was written into it unchanged, so a
  half-full 24 MWh unit started at 0.5 MWh. The fraction is now applied to
  the installed energy (`p_nom` × `max_hours`); an expandable storage is
  told it starts from its existing capacity.

## [0.1.0-alpha] - 2026-10-24

The first pre-release. It carries the service as scaffolded in July 2026 —
the canonical model and its validation, the emission toolkit, the target
registry with capability-driven validation, the PyPSA, Calliope and
AdOpT-NET0 targets, the job execution engine with bounded concurrency, the
HTTP API with multi-target resolution and API-key authentication, the
embedded scenario corpus with golden snapshots and the validation matrix, the
containerized solver environment, the real-solver E2E suite, the published
JSON Schemas and the example payloads — together with everything that landed
on it since, listed below. Before 1.0 the API and the canonical model may
still change between releases.

### Added

- `meme -version` and the `version` field of `GET /healthz` report the build
  version, which `make build` stamps from the git tag.
- Calliope 0.6.8 as the `calliope-0.6` target: its own emitter for the 0.6
  model definition (`model`/`run`/`techs`/`locations`/`links`, `file=`
  timeseries, `group_constraints`, `conversion_plus` carrier tiers), its own
  capability profile (k-means typical days and an emissions-minimizing
  objective gained; indexed parameters, piecewise performance and custom
  math absent) and native key `native["calliope-0.6"]` ([#4]).
- Framework versions as separate targets: a default per framework, version
  variants selected by name, version-explicit aliases (`calliope-0.7`), and
  `GET /capabilities` reporting `default_targets`, `aliases` and `versions`.
- Environment image with one virtual environment per Calliope release,
  provisioned by `uv` (Calliope 0.6.8 runs on a managed Python 3.8).
- Scenarios and cross-framework E2E checks for multi-node technologies
  (`calliope_multi_node`, `calliope_multi_node_supply`) and operate-mode
  limits (`calliope_operate_limits`); the same payload is pinned to the same
  objective on PyPSA, Calliope 0.7 and Calliope 0.6.
- A CI workflow (format, vet, race tests, build, schema check, vulnerability
  scan) and a strict documentation build deployed to GitHub Pages.
- Validation of `timeseries.source` (`inline` with values, `file` with a
  path) and rejection of a node listed twice on one technology.

### Changed

- Calliope 0.7 target migrated from 0.7.0.dev7 to the stable **0.7.0**
  release: data tables are spelled `table` / `add_dims.inputs`, a supply
  technology's fuel cost is `cost_source_use`, ratio-pinning math no longer
  uses the removed `defined()` helper, and node coordinates are emitted on
  all nodes or none ([#3]).
- Calliope data tables are named after technology and parameter
  (`<tech>__<param>.csv`) instead of the series id two technologies could
  share.
- The documentation moved from one long README into a MkDocs site; the
  capability reference lives at `docs/reference/capability-reference.md`.

### Removed

- The organisation-template workflows `sync-legal.yml` and
  `sync-branch-rules.yml` with their scripts: run from this repository they
  acted on every repository of two GitHub organisations and needed the
  template's tokens, so every run here failed. The template-only pages on
  legal sync and branch rules left the documentation site; the branch-naming
  and commit-convention pages stay, reworded for MEME, and the lint scripts
  point at them.

### Fixed

- Calliope: a technology placed at several nodes wrote its demand profile,
  availability or source series for the first node only; every placed node
  now gets its own data-table column, and per-node profile overrides land in
  their node's column ([#7]).
- Calliope: in operate mode a trade import ran unbounded and a transmission
  line ignored its capacity; both are installed at their fixed size, as are
  `source_cap` and `area_use`, with a warning where `capacity.max` stands in
  for the installed size, and a model with no fixed capacity is rejected
  before the solve instead of aborting after it ([#8]). The same gap was
  closed on the 0.6 target, where the import ran above its limit.
- Calliope: a transmission line's `capacity.existing` is honored in plan mode
  (it used to be ignored, leaving a fixed line unbounded).
- Calliope: an already-annualized cost basis pins `cost_depreciation_rate: 1`
  instead of being divided by the lifetime again.
- PyPSA: a payload without inline series is solved over the whole
  `model.time` range instead of a single default snapshot.
- A file-backed time series, which no target can materialize, is rejected
  at validation instead of failing at emit time.
- Container: the API service restarts unless stopped; development tooling
  corrected and `tsam` pinned below 4 for AdOpT-NET0.
- Earlier fixes: CORS validation for credentialed wildcards and reading the
  CORS setting from the environment; solver process-group cleanup, output
  capping and bundle symlinks; capability gates on node overrides; numeric
  edge cases, resolution parsing and YAML floats in the emitters; a node
  with no technologies emitted as `techs: {}`.

[Unreleased]: https://github.com/enerplanet/meme/compare/v0.2.0-alpha...HEAD
[0.2.0-alpha]: https://github.com/enerplanet/meme/compare/v0.1.0-alpha...v0.2.0-alpha
[ADR-0008]: https://enerplanet.github.io/meme/decisions/0008-power-flow-as-a-pypsa-run-mode/
[ADR-0009]: https://enerplanet.github.io/meme/decisions/0009-reactive-power-voltage-control-and-fixed-transfers/
[ADR-0010]: https://enerplanet.github.io/meme/decisions/0010-electrical-network-inside-optimization/
[ADR-0011]: https://enerplanet.github.io/meme/decisions/0011-pypsa-optimization-completeness/
[0.1.0-alpha]: https://github.com/enerplanet/meme/releases/tag/v0.1.0-alpha
[#3]: https://github.com/enerplanet/meme/issues/3
[#4]: https://github.com/enerplanet/meme/issues/4
[#7]: https://github.com/enerplanet/meme/issues/7
[#8]: https://github.com/enerplanet/meme/issues/8
