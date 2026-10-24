![MEME banner](docs/assets/logos/meme-banner-dark.png#gh-dark-mode-only)
![MEME banner](docs/assets/logos/meme-banner-light.png#gh-light-mode-only)

# Multi Energy Model Execution (MEME)

[![CI](https://github.com/enerplanet/meme/actions/workflows/ci.yml/badge.svg)](https://github.com/enerplanet/meme/actions/workflows/ci.yml)
[![MkDocs](https://github.com/enerplanet/meme/actions/workflows/docs.yml/badge.svg)](https://enerplanet.github.io/meme)
[![Release](https://img.shields.io/github/v/release/enerplanet/meme?include_prereleases&sort=semver&display_name=tag)](https://github.com/enerplanet/meme/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/enerplanet/meme?logo=go&logoColor=white)](go.mod)
[![License: MIT](https://img.shields.io/github/license/enerplanet/meme)](LICENSE)

MEME is a dependency-free Go service (standard library only) that accepts one
**canonical JSON energy-system model** and translates it into the native input
formats of three optimization frameworks — [PyPSA](https://pypsa.org),
[Calliope](https://www.callio.pe) (release 0.7, with 0.6 as a selectable
version) and [AdOpT-NET0](https://github.com/UU-ER/AdOpT-NET0) — then
optionally runs the solve and returns a zipped result bundle. A request targets
one framework, a subset, or all of them at once; the service is a stateless
translator and orchestrator, the solvers are external Python processes.

```
Client ──POST /simulate?target=pypsa,calliope──▶ MEME ── validate (capability gates)
       ◀──202 {id}────────────────────────────    │   ── emit native model files
Client ──GET /jobs/{id}/status──────────────────   ▼   ── run the solver (python / calliope CLI)
       ◀──state · log · per-run results────────  job ── bundle: results + inputs + config + log
Client ──GET /jobs/{id}───────────────────────▶ zip
```

**Documentation: <https://enerplanet.github.io/meme>** — quickstart, API
reference, the canonical model, what each framework honors property by
property, architecture and the decisions behind it.

## How it works

1. `POST /validate|/convert|/simulate?target=…` receives the canonical
   payload. `target` is one framework, a comma-separated list, or `all` (the
   default version of every framework). Validation is **capability-gated**:
   a property the selected target cannot honor is rejected with a pointed
   message or named in a warning — never silently dropped.
2. The target's emitter writes the framework's native model: a PyPSA CSV
   folder, a Calliope `model.yaml` with data tables, an AdOpT `input_data/`
   tree. Anything only one framework understands rides in that framework's
   `native` block and is merged verbatim.
3. `/simulate` schedules an asynchronous job: each run is solved by an
   external process (a generated `run.py`, or the `calliope` CLI), sweeps
   expand into independent runs, and `GET /jobs/{id}` returns one zip with
   solver results, emitted inputs, driver scripts, the log and the submitted
   config.

## Targets

| Target | Framework | Version | In `all` |
|---|---|---|:--:|
| `pypsa` | PyPSA | 1.2.4 | ✓ |
| `calliope` (alias `calliope-0.7`) | Calliope 0.7 | 0.7.0 | ✓ |
| `calliope-0.6` | Calliope 0.6 | 0.6.8 | by name |
| `adopt-net0` | AdOpT-NET0 | 0.1.10 | ✓ |

`GET /capabilities` lists the targets, what `all` runs, the aliases, the
framework version each target drives, and the feature matrix. One run mode is
not an optimization: `experiment.mode: power_flow` has PyPSA solve the AC
network described in the payload (voltage levels, line and transformer
impedances, a fixed dispatch) and report voltages, loading and convergence,
the natural second call after a plan on any target.

## Quickstart

Dry-run on the host (Go only — validates, translates and plans, no solve):

```bash
make run                                                        # build + serve on :8080
curl -s -X POST 'localhost:8080/validate?target=all' -d @examples/shared_full.json | jq .
curl -s -X POST 'localhost:8080/convert?target=calliope' -d @examples/calliope_full.json | jq .
```

Without a checkout, `go install github.com/enerplanet/meme/cmd/meme@latest`
puts the same dry-run binary into your Go bin directory (`meme -version`
tells you which release).

Real solvers in Docker (PyPSA/HiGHS, Calliope 0.7 and 0.6/CBC, AdOpT/GLPK):

```bash
make -C environment build ENV=dev                               # one-time image build
make -C environment run   ENV=dev                               # API on :8080, real execution
ID=$(curl -s -X POST 'localhost:8080/simulate?target=all' -d @examples/shared_full.json | jq -r .id)
curl -s "localhost:8080/jobs/$ID/status" | jq .state            # queued -> running -> succeeded
curl -s -o result.zip "localhost:8080/jobs/$ID"
```

Configuration (ports, work directory, `API_KEY`, `CORS_ORIGINS`) comes from
CLI flags or a `.env` file; see [`.env.example`](.env.example) and the
[configuration guide](https://enerplanet.github.io/meme/getting-started/configuration/).

## Repository layout

```
cmd/meme/          server main
internal/          api · cli · env · model · emit · scripts · target/{pypsa,calliope,adoptnet0} · scenarios · service
test/              test pyramid (host suites, goldens, real-solver E2E)
environment/       Docker image with the frameworks, their venvs and free solvers
examples/          ready-to-run payloads and the curl walkthrough
schemas/           the published JSON Schemas (payload contract)
docs/              the documentation site (MkDocs)
```

## Development

```bash
make test           # vet + host suite: unit tests, golden files, validation matrix (no Python)
make schema-check   # examples and the scenario corpus against the JSON Schema
make e2e            # container: every scenario on real solvers, both Calliope releases
make docs           # build the documentation site strictly
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow, [CHANGELOG.md](CHANGELOG.md)
for what changed, and the [testing guide](https://enerplanet.github.io/meme/operations/testing/)
for the test pyramid.

## License

MIT — see [LICENSE](LICENSE). Third-party frameworks and solvers used by the
environment image are listed in [ATTRIBUTIONS.md](ATTRIBUTIONS.md). To cite
MEME, use [CITATION.cff](CITATION.cff).
