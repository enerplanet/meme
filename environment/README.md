# `environment/` — containerized build & test

A single image that carries everything needed to **build**, **test**, and
**run** the `meme` (energymodel) service end-to-end:

| Layer | What |
|---|---|
| Go 1.22 toolchain | build the stdlib-only service, run `go test ./...` |
| Python 3.12 | host for the frameworks (AdOpT-NET0 requires ≥ 3.12) |
| **PyPSA 1.2.4**, **AdOpT-NET0 0.1.10** | in the system Python (driven via the generated `run.py`) |
| **Calliope 0.7.0.dev7** | in its own venv (`/opt/calliope-venv`; its pins conflict with the above) — only the `calliope` CLI is exposed on `PATH` |
| **HiGHS** (`highspy`) | solves PyPSA (`solver.name: "highs"`, the default) |
| **CBC** (apt `coinor-cbc`) | solves Calliope — HiGHS cannot drive Calliope 0.7.dev7 (pyomo persistent-interface bug), so `highs` is mapped to `cbc` with a warning |
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

The targets live in this folder's [`Makefile`](Makefile): run them **inside
`environment/`** as plain `make <target>`, or from the repo root as
`make -C environment <target>`. There are deliberately no `docker-*` aliases
in the root Makefile:

```bash
# from the repo root:
make -C environment build ENV=dev   # image (frameworks + solvers + Go)
make -C environment test  ENV=dev   # full Go suite inside the container
make -C environment run   ENV=dev   # API with real solvers
make -C environment shell ENV=dev   # go/python/pypsa/calliope/adopt/solvers

# End-to-end tiers (real HTTP API + frameworks + solvers; defined in
# test/Makefile, aliased at the root):
make e2e-smoke               # 3 per-target lifecycles + one multi-target job (~1 min)
make e2e                     # the whole scenario corpus, -parallel 3 (~1.5 min)
```

See [`test/README.md`](../test/README.md) for what the E2E tiers assert.

## Per-environment settings (dev / prod)

Ports, the work dir, and the image tag are read from an env file rather than
hard-coded in the compose file:

These use the **same variable names** as the API's own `.env` (see
[`.env.example`](../.env.example)), so one vocabulary works whether you run via
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

- Framework versions are pinned in [`requirements.txt`](requirements.txt)
  (PyPSA + AdOpT, system Python) and
  [`requirements-calliope.txt`](requirements-calliope.txt) (Calliope venv,
  including its pandas/pyomo compatibility caps). The pins are load-bearing:
  the emitters and run scripts are validated against exactly these versions —
  Calliope in particular must stay at `0.7.0.dev7` (dev8 renames data-table
  keys). Bump deliberately and re-run the E2E suite.
- CBC and GLPK are not fallbacks — they are the primary solvers for Calliope
  and AdOpT respectively (see the table above). Note the solvers themselves are
  not exact-pinned: `highspy` is unversioned in the requirements and CBC/GLPK
  come from apt — only the three framework packages carry `==` pins.
- The `api` service writes emitted files to `/src/.work`; add that to
  `.gitignore` if you don't already ignore it. Note the container runs as root,
  so `.work` contents created via docker are root-owned on the host.
