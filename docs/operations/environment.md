# Environment

A single image that carries everything needed to **build**, **test**, and
**run** the `meme` (energymodel) service end-to-end:

| Layer | What |
|---|---|
| Go 1.22 toolchain | build the stdlib-only service, run `go test ./...` |
| Python 3.12 | host for the frameworks (AdOpT-NET0 requires ≥ 3.12) |
| **PyPSA 1.2.4**, **AdOpT-NET0 0.1.10** | in the system Python (driven via the generated `run.py`) |
| **Calliope 0.7.0** | in its own venv (`/opt/calliope-0.7-venv`, Python 3.12; its pins conflict with the above) — exposed on `PATH` as `calliope` (target `calliope`) and `calliope-0.7` (the version-explicit alias) |
| **Calliope 0.6.8** | in a second venv (`/opt/calliope-0.6-venv`) on a **uv-managed Python 3.8** (its pins — numpy 1.20, pandas 1.3, pyomo 6.2, scikit-learn 0.23 — have no 3.12 wheels) — exposed as `calliope-0.6` (target `calliope-0.6`) |
| **uv** | provisions the venvs and the extra interpreter (kept under `/opt/uv-python`; the system Python is untouched) |
| **HiGHS** (`highspy`) | solves PyPSA (`solver.name: "highs"`, the default) |
| **CBC** (apt `coinor-cbc`) | solves both Calliope releases — HiGHS cannot drive Calliope's pyomo backend, so `highs` is mapped to `cbc` with a warning |
| **GLPK** (apt `glpk-utils`) | solves AdOpT-NET0 |

With all of the above on `PATH`, the service runs in real-execution mode
(`meme -exec`), so `POST /simulate` actually invokes the solvers instead of
dry-running. This is the exact version set the E2E suite certifies — every
scenario solves to optimal on it.

## Setup

Prerequisites: **Docker with compose v2** and GNU `make` — everything else
(Go, Python, the frameworks, the solvers) lives inside the image. From the
repo root (or inside `environment/`, dropping the `-C environment`):

```bash
make -C environment build ENV=dev   # one-time image build (~framework install; grab a coffee)
make -C environment run   ENV=dev   # API with real solver execution on http://localhost:8080
```

The repo root is bind-mounted at `/src`, so source edits are picked up without
rebuilding the image. Only the (heavy) framework/solver layer is baked in; the
Go build and module caches persist in named volumes across runs — the first
containerized `test` run compiles everything, later runs are incremental. Rebuild the
image only when a `requirements*.txt` or the Dockerfile changes.

## Usage

The targets live in this folder's [`Makefile`](https://github.com/enerplanet/meme/blob/main/environment/Makefile): run them **inside
`environment/`** as plain `make <target>`, or from the repo root as
`make -C environment <target>`. There are deliberately no `docker-*` aliases
in the root Makefile:

```bash
# from the repo root:
make -C environment build ENV=dev   # image (frameworks + solvers + Go)
make -C environment test  ENV=dev   # full Go suite inside the container
make -C environment run   ENV=dev   # API with real solvers
make -C environment shell ENV=dev   # go/python/pypsa/calliope (0.7 + 0.6)/adopt/solvers

# End-to-end tiers (real HTTP API + frameworks + solvers; defined in
# test/Makefile, aliased at the root):
make e2e-smoke               # 4 per-target lifecycles (incl. calliope-0.6), one multi-target job, the PyPSA feature checks (~30 s)
make e2e                     # the whole scenario corpus and all examples, -parallel 3 (~2 min)
```

See [Testing](testing.md) for what the E2E tiers assert.

## Per-environment settings (dev / prod)

Ports, the work dir, and the image tag are read from an env file rather than
hard-coded in the compose file:

These use the **same variable names** as the API's own `.env` (see
[`.env.example`](https://github.com/enerplanet/meme/blob/main/.env.example)), so one vocabulary works whether you run via
compose or invoke the binary directly with `meme -env-file environment/.env.dev`.

| Variable | `.env.dev` | `.env.prod` | Meaning | Used by |
|---|---|---|---|---|
| `PORT` | 8080 | 8080 | port the API listens on inside the container | compose + API |
| `HOST_PORT` | 8080 | 80 | port published on your machine | compose |
| `WORK` | `/src/.work` | `/src/.work` | emitted-files dir | compose + API |
| `EXEC` | true | true | run real solvers vs. dry-run | compose + API |
| `API_KEY` | *(unset)* | *(unset)* | require this key in every request payload; unset = auth off | compose + API |
| `CORS_ORIGINS` | *(unset)* | *(unset)* | browser origins allowed via CORS, comma-separated (`*` and `https://*.sub` wildcards); unset = CORS off | compose + API |
| `IMAGE_TAG` | `meme-env:dev` | `meme-env:prod` | image tag | compose |

Select one with `ENV=` on any of this folder's Make targets (defaults to
`dev`):

```bash
# from the repo root:
make -C environment run ENV=dev     # publishes on :8080
make -C environment run ENV=prod    # publishes on :80
```

Or drive compose directly with `--env-file`:

```bash
docker compose --env-file environment/.env.prod \
  -f environment/docker-compose.yml up api
```

Copy either file to add more environments (e.g. `.env.staging`) and select it
with `ENV=staging`.

## Notes

- Framework versions are pinned in [`requirements.txt`](https://github.com/enerplanet/meme/blob/main/environment/requirements.txt)
  (PyPSA + AdOpT, system Python),
  [`requirements-calliope-0.7.txt`](https://github.com/enerplanet/meme/blob/main/environment/requirements-calliope-0.7.txt) (the
  `calliope` target) and
  [`requirements-calliope-0.6.txt`](https://github.com/enerplanet/meme/blob/main/environment/requirements-calliope-0.6.txt) (the
  `calliope-0.6` target, with upstream's own dependency caps). The pins are
  load-bearing: each emitter is written against one model-definition dialect
  (0.7.0 spells data tables `table`/`add_dims.inputs` and dropped the
  `defined()` math helper that dev7 accepted; 0.6 is a different document
  altogether), so a Calliope bump is an emitter change, not just a pin
  change. `GET /capabilities` reports the version each target drives. Bump
  deliberately and re-run the E2E suite.
- The two Calliope venvs are separate image layers, so bumping one pin
  rebuilds only that venv. Calliope 0.6.8 needs Python ≤ 3.8 because its
  scikit-learn cap (`<0.24`, required for k-means clustering) has no newer
  wheels; `uv` downloads that interpreter at build time.
- CBC and GLPK are not fallbacks — they are the primary solvers for Calliope
  and AdOpT respectively (see the table above). Note the solvers themselves are
  not exact-pinned: `highspy` is unversioned in the requirements and CBC/GLPK
  come from apt — only the three framework packages carry `==` pins.
- The Compose project is `meme`, so the API container is `meme-api-1`. `api`
  also joins the shared `tentacron-net` network, where services in other
  repositories address it as `meme`; `meme-env-api-1` still resolves there for
  callers not yet moved. The `test` and `shell` services sit under the `tools`
  profile, so `up` starts only `api`; the Makefile targets name them and run
  them as before.
- The `api` service writes emitted files to `/src/.work`; add that to
  `.gitignore` if you don't already ignore it. Note the container runs as root,
  so `.work` contents created via docker are root-owned on the host.
