# `environment/` — containerized build & test

One Docker image carries everything needed to build, test and run MEME with
real solvers: the Go toolchain, Python 3.12 with PyPSA and AdOpT-NET0, one
virtual environment per Calliope release (0.7.0 as `calliope`, 0.6.8 as
`calliope-0.6`, provisioned by `uv`), and the free solvers HiGHS, CBC and
GLPK. The repository is bind-mounted, so source edits need no rebuild; only a
change to the `Dockerfile` or a `requirements*.txt` does.

```bash
make -C environment build ENV=dev   # one-time image build
make -C environment run   ENV=dev   # API with real solvers on http://localhost:8080
make -C environment test  ENV=dev   # the Go suite inside the container
make -C environment shell ENV=dev   # interactive shell with everything on PATH
```

`ENV=dev` (default) or `ENV=prod` selects `.env.dev` / `.env.prod`; the same
variable names configure the API binary directly (`meme -env-file
environment/.env.dev`).

The full guide — image layers, the per-release Calliope venvs and why 0.6.8
needs Python 3.8, the env-file vocabulary, pin policy — is in the
documentation: **<https://enerplanet.github.io/meme/operations/environment/>**.
