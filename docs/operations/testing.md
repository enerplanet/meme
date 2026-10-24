# Testing

Everything about testing MEME lives behind
[`test/Makefile`](https://github.com/enerplanet/meme/blob/main/test/Makefile); the root Makefile aliases every target, so
`make test` and `make -C test test` are the same thing.

```bash
make test           # vet + all unit/integration tests on the host (no Python)
make test-race      # race-detector pass over the concurrent packages
make schema-check   # examples/ + scenario corpus vs schemas/revised_unified_schema.json (python3 + jsonschema)
make e2e-smoke      # container: 4 real-solver lifecycles (incl. calliope-0.6) + one multi-target job (~1 min)
make e2e            # container: the full corpus on real solvers, both Calliope releases (~2 min)
make golden-update  # accept an intended emitter/validation change into the goldens
```

The suite is a pyramid: golden-file emitter snapshots and a frozen validation
matrix catch most regressions **without solvers, in milliseconds**; the
container tiers certify real execution end-to-end (native files on disk,
solver optimal, complete zip bundle, pinned objective values).
The rest of this page is the full tour: what each layer covers, the golden
workflow, how to add a scenario, fuzzing.

Worked example payloads for every framework's use cases (generators, conversion,
storage, transmission, trade with fixed and time-varying prices, unit commitment
(PyPSA UC + Calliope MILP), emission limits, scoped + unscoped linear
constraints, custom math, SPORES / MGA, operate (Calliope receding horizon +
PyPSA rolling horizon), two-stage stochastic, physics/climate, per-node
overrides, precomputed physics, multi-output CHP with rigid ratios, piecewise
part-load performance, resampling, Pareto / Monte-Carlo / typical-days runs)
live in [`internal/scenarios/testdata/scenarios/`](https://github.com/enerplanet/meme/tree/main/internal/scenarios/testdata/scenarios/) and are
exercised by `TestScenarios` (validate + emit) and by the E2E suite (each
scenario runs to optimal on its real solver). `TestCapabilityCoverage` enforces
that every capability the matrix claims is backed by one of these scenarios —
an unbacked claim fails the build. See the
[scenario catalog](../scenarios.md) for the full list.
`TestAdoptRunModeConfig` additionally parses the emitted `ConfigModel.json` to
prove the Pareto/Monte-Carlo/typical-days modes reach AdOpT's solve
configuration.

## Setup

- **Host tiers** (`test`, `test-race`, `golden-update`): Go ≥ 1.22, nothing
  else. No Python, no solvers — the default executor is a dry-runner.
- **Schema tier** (`schema-check`): python3 with `jsonschema` ≥ 4.18. Validates
  [`schemas/revised_unified_schema.json`](https://github.com/enerplanet/meme/blob/main/schemas/revised_unified_schema.json)
  against the draft 2020-12 meta-schema and every payload in
  [`examples/`](https://github.com/enerplanet/meme/tree/main/examples/) plus the embedded scenario corpus against it —
  the drift gate between the published contract and the hand-written Go
  validation (`internal/model`). The Go module itself stays dependency-free.
- **Container tiers** (`e2e-smoke`, `e2e`): Docker with compose v2 and the
  environment image built once via `make -C environment build ENV=dev` (see
  [environment guide](environment.md)). The Make targets
  mount the repo at `/src`, set `MEME_E2E=1`, and cap solver processes with
  `-parallel 3`; source edits are picked up without an image rebuild.

## The layers, bottom to top

### 1. Unit tests (host, milliseconds)

Live next to their packages:

| Area | What is proven |
|---|---|
| `internal/emit` | CRF annualization (incl. zero-interest limit), snapshot labels, date/resolution parsing, series padding, CSV quoting, native deep-merge + `FuzzMergeNativeMap` |
| `internal/model` | schema round-trip, structural validation |
| `internal/target` | registry resolution, the 4-stage validation pipeline (structural → generic capability gates → per-target hooks → native warnings), capability-coverage meta-test |
| `internal/target/{pypsa,calliope,adoptnet0}` | per-emitter behavior: buses/links/UC columns, YAML quoting (+ `FuzzYAMLString`), custom math, dbTech mapping, node overrides |
| `internal/service` | executor concurrency bound (peak ≤ MaxJobs), `CommandRunner` timeout/cancel kills the process, multi-target layout, **partial multi-target failure keeps surviving targets' bundles**, log write-through, job GC (incl. churn under `-race`) |
| `internal/api` | async lifecycle on a dry-runner, multi-target endpoints, error envelope (400/404/405/409/413/422), body size cap, `/v1` alias, **api_key enforcement + bundle redaction**, byte-identical `config.json` |
| `internal/cli`, `internal/env` | config precedence (flag > .env > default), .env parsing |

### 2. Golden files (host, milliseconds) — the regression tripwire

- **Emit goldens** — [`internal/target/golden_test.go`](https://github.com/enerplanet/meme/blob/main/internal/target/golden_test.go):
  every embedded scenario × every target that accepts it is emitted
  (`Emit` + `Plan`, generated driver script included) into a temp dir and
  compared **byte-for-byte** against
  [`internal/target/testdata/golden/`](https://github.com/enerplanet/meme/tree/main/internal/target/testdata/golden/)
  (~490 files). The one absolute path is normalized to `${BASE}`.
- **Validation matrix golden** — `testdata/golden/validation_matrix.json`:
  the exact verdict (`ok` / error text) and every warning string for each
  scenario × target pair, frozen in one JSON.

Workflow: change an emitter or a gate → `make test` fails with a plain text
diff naming file and line → if the change is intended, `make golden-update`
regenerates and re-verifies; **review the golden diff as part of the change**,
it *is* the contract change. Emission is deterministic by construction (all
map iterations are sorted), so goldens never flake.

### 3. E2E (container, real solvers) — [`test/e2e/`](https://github.com/enerplanet/meme/tree/main/test/e2e/)

Gated by `MEME_E2E=1` (set by the Make targets); outside the container every
test self-skips. Two tiers, selected with Go's standard `-short` flag:

- **Smoke** (`make e2e-smoke`, ~1 min): one full lifecycle per target
  (submit → poll → files on disk → solver optimal → complete zip) — PyPSA,
  Calliope 0.7 (`calliope`), Calliope 0.6 (`calliope-0.6`, the second venv's
  `calliope-0.6` CLI) and AdOpT — plus one `?target=all` job (the 3 default
  frameworks × 2 sweep points in a single bundle; `all` never runs two
  Calliope releases unless named), and the three **power-flow** checks on
  PyPSA (`TestE2EPyPSAPowerFlow*`: Newton-Raphson converging on every
  snapshot with plausible voltages and loadings, the linear method, and a
  diverging load that must fail the run with exit code 3). The lifecycles
  also pin the **objective value**: each `objectivePin` carries the recorded
  optimum and its own relative delta (`rel: 1e-4` = ±0.01%) absorbing solver
  numerics — drift beyond it is a translation regression. The two Calliope
  lifecycles solve the same `calliope_plan.json`, so their pins must agree.
- **Full** (`make e2e`, ~2 min): adds the economics pins (export revenue
  sign, time-varying import prices, binding sidecar constraint), the
  cross-target consistency check (the same model must reach the same
  objective on all three frameworks ±1%), the failure path (infeasible model
  → `state=failed`), **every scenario in the corpus** run to optimal (or, for
  `pypsa_power_flow`, to convergence) on its prefix target, every
  `calliope_*` scenario the 0.6 gates accept run again on `calliope-0.6`
  (`TestE2ECalliope06Corpus`), and **every payload in `examples/`** run to
  `succeeded` on its target (`TestE2EExamplesSolve`).
  Scenario subtests are `t.Parallel()` with an isolated server + work dir
  each; `-parallel 3` bounds concurrent solver processes.

## Adding coverage

- **New capability / scenario**: drop a runnable payload into
  [`internal/scenarios/testdata/scenarios/`](https://github.com/enerplanet/meme/tree/main/internal/scenarios/testdata/scenarios/)
  (name prefix = its target: `pypsa_*`, `calliope_*` (0.7), `calliope06_*`
  (0.6-specific, e.g. k-means typical days), `adopt_*`). It is
  automatically picked up by the corpus integrity test, the emit goldens
  (run `make golden-update` once), the validation matrix, and the full E2E
  sweep. If the scenario backs a new capability-matrix claim, map it in
  `featureScenarios` (`internal/target/coverage_test.go`) — an unbacked
  claim fails the build.
- **New objective pin**: run the scenario once in the container, read the
  logged `objective <value>` line, record it in an `objectivePin` with a
  delta wide enough for solver numerics (LPs: `1e-4`; anything with a MIP
  gap: match the gap).
- **Fuzzers**: `go test ./internal/emit -run xxx -fuzz FuzzMergeNativeMap -fuzztime 30s`
  (same pattern for `FuzzYAMLString` in `internal/target/calliope`). Both have
  already caught real bugs; run them after touching a writer.

## Notes

- The container runs as root; job dirs the E2E suite writes under the mounted
  repo (`.work/`) are root-owned on the host.
- A full-suite pass certifies the exact framework/solver versions pinned in
  [`environment/requirements*.txt`](https://github.com/enerplanet/meme/tree/main/environment/) — re-run `make e2e`
  after any pin bump. Both Calliope releases are certified: the goldens under
  `testdata/golden/calliope/` are 0.7.0 models, those under
  `testdata/golden/calliope-0.6/` are 0.6.8 models.
