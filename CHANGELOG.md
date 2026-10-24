# Changelog

All notable changes to MEME are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org), and git tags carry the `v` prefix
Go modules require (`v0.1.0-alpha`).

## [Unreleased]

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
  capability (PyPSA only); a `dispatch` in any optimization mode is rejected
  on every target with a pointer to `operation.equals_pu`; electrical
  parameters outside a power flow are named as unused while the arcs stay
  transport links; in mode `power_flow` expandable capacities, trade,
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

### Changed

- Power-flow inputs — `dispatch`, the `electrical` blocks on technologies,
  arcs and nodes — are ignored with one warning wherever no power flow runs:
  on Calliope and AdOpT-NET0 and in PyPSA's optimization modes alike. A
  `dispatch` in an optimization mode was rejected before; now one payload
  serves the planning call and the power-flow call ([ADR-0009]).
- The PyPSA driver parses its run configuration as JSON (`json.loads`)
  instead of pasting the JSON in as a Python literal, which broke on the
  first boolean setting.

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

[Unreleased]: https://github.com/enerplanet/meme/compare/v0.1.0-alpha...HEAD
[ADR-0008]: https://enerplanet.github.io/meme/decisions/0008-power-flow-as-a-pypsa-run-mode/
[ADR-0009]: https://enerplanet.github.io/meme/decisions/0009-reactive-power-voltage-control-and-fixed-transfers/
[0.1.0-alpha]: https://github.com/enerplanet/meme/releases/tag/v0.1.0-alpha
[#3]: https://github.com/enerplanet/meme/issues/3
[#4]: https://github.com/enerplanet/meme/issues/4
[#7]: https://github.com/enerplanet/meme/issues/7
[#8]: https://github.com/enerplanet/meme/issues/8
