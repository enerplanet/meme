# `test/` — the test pyramid

Everything about testing the meme (energymodel) service lives behind
[`test/Makefile`](Makefile); the root Makefile aliases every target, so
`make test` and `make -C test test` are the same thing.

```bash
make test           # vet + all host tests (pure Go, no Python; seconds)
make test-race      # race detector over internal/... (executor, job store, API)
make golden-update  # regenerate golden files after an INTENDED emitter change
make schema-check   # payload contract: examples/ + scenario corpus vs the JSON Schema
make e2e-smoke      # container: real-solver smoke tier (~1 min)
make e2e            # container: full corpus on real solvers (~1.5 min)
```

## Setup

- **Host tiers** (`test`, `test-race`, `golden-update`): Go ≥ 1.22, nothing
  else. No Python, no solvers — the default executor is a dry-runner.
- **Schema tier** (`schema-check`): python3 with `jsonschema` ≥ 4.18. Validates
  [`docs/revised_unified_schema.json`](../docs/revised_unified_schema.json)
  against the draft 2020-12 meta-schema and every payload in
  [`examples/`](../examples/) plus the embedded scenario corpus against it —
  the drift gate between the published contract and the hand-written Go
  validation (`internal/model`). The Go module itself stays dependency-free.
- **Container tiers** (`e2e-smoke`, `e2e`): Docker with compose v2 and the
  environment image built once via `make -C environment build ENV=dev` (see
  [`environment/README.md`](../environment/README.md)). The Make targets
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

- **Emit goldens** — [`internal/target/golden_test.go`](../internal/target/golden_test.go):
  every embedded scenario × every target that accepts it is emitted
  (`Emit` + `Plan`, generated driver script included) into a temp dir and
  compared **byte-for-byte** against
  [`internal/target/testdata/golden/`](../internal/target/testdata/golden/)
  (~490 files). The one absolute path is normalized to `${BASE}`.
- **Validation matrix golden** — `testdata/golden/validation_matrix.json`:
  the exact verdict (`ok` / error text) and every warning string for each
  scenario × target pair, frozen in one JSON.

Workflow: change an emitter or a gate → `make test` fails with a plain text
diff naming file and line → if the change is intended, `make golden-update`
regenerates and re-verifies; **review the golden diff as part of the change**,
it *is* the contract change. Emission is deterministic by construction (all
map iterations are sorted), so goldens never flake.

### 3. E2E (container, real solvers) — [`test/e2e/`](e2e/)

Gated by `MEME_E2E=1` (set by the Make targets); outside the container every
test self-skips. Two tiers, selected with Go's standard `-short` flag:

- **Smoke** (`make e2e-smoke`, ~1 min): one full lifecycle per framework
  (submit → poll → files on disk → solver optimal → complete zip) plus one
  `?target=all` job (3 frameworks × 2 sweep points in a single bundle). The
  lifecycles also pin the **objective value**: each `objectivePin` carries
  the recorded optimum and its own relative delta (`rel: 1e-4` = ±0.01%)
  absorbing solver numerics — drift beyond it is a translation regression.
- **Full** (`make e2e`, ~1.5 min): adds the economics pins (export revenue
  sign, time-varying import prices, binding sidecar constraint), the
  cross-target consistency check (the same model must reach the same
  objective on all three frameworks ±1%), the failure path (infeasible model
  → `state=failed`), and **every scenario in the corpus** run to optimal.
  Scenario subtests are `t.Parallel()` with an isolated server + work dir
  each; `-parallel 3` bounds concurrent solver processes.

## Adding coverage

- **New capability / scenario**: drop a runnable payload into
  [`internal/scenarios/testdata/scenarios/`](../internal/scenarios/testdata/scenarios/)
  (name prefix = its target: `pypsa_*`, `calliope_*`, `adopt_*`). It is
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
  [`environment/requirements*.txt`](../environment/) — re-run `make e2e`
  after any pin bump.
