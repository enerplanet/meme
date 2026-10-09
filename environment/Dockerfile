# syntax=docker/dockerfile:1
#
# Build-and-test environment for the meme (energymodel) service.
#
# One image that carries everything needed end-to-end:
#   * Go 1.22 toolchain  -> build the stdlib-only service and run `go test`
#   * Python 3.12        -> the three frameworks the service emits for
#   * PyPSA + HiGHS, Calliope 0.7 + CBC, AdOpT-NET0 + GLPK (all free solvers)
#
# With all of the above on PATH, the service can run in real-execution mode
# (`meme -exec`) so /simulate actually invokes the solvers, and integration
# tests can validate emitted files against pinned framework versions.

# Stages:
#   go         Go toolchain (also used to compile the release binary)
#   frameworks Python + PyPSA/Calliope/AdOpT + solvers (shared base)
#   build      compiles the static `meme` binary
#   prod       frameworks + binary, non-root, healthcheck  <- VM deployment
#   dev        frameworks + Go toolchain, source bind-mounted  <- default (last)
#
#   docker build -f environment/Dockerfile --target prod .

# ---- Go toolchain ----
FROM golang:1.22-bookworm AS go

# ---- Python base + frameworks + solvers ----
# Python 3.12: AdOpT-NET0 (adopt_net0) requires >=3.12.
FROM python:3.12-slim-bookworm AS frameworks

# System libraries the frameworks need at runtime:
#   git            - some pip deps resolve VCS refs
#   build-essential- native wheels that fall back to source
#   coinor-cbc / glpk - primary solvers for Calliope and AdOpT-NET0
#                    respectively (HiGHS covers PyPSA only)
#   libspatialindex - geo dep pulled in by pypsa/calliope
RUN apt-get update && apt-get install -y --no-install-recommends \
        git \
        make \
        build-essential \
        coinor-cbc \
        glpk-utils \
        libspatialindex-dev \
    && rm -rf /var/lib/apt/lists/* \
    && git config --system --add safe.directory /src

# Install the Python frameworks + solver. Cached as its own layer so source
# changes don't reinstall the (heavy) Python stack.
# NOTE: paths are relative to the build context, which is the repo root
# (compose sets context: ..), so the env files live under environment/.
#
# PyPSA + AdOpT go in the system Python (both are imported via `python -c ...`).
COPY environment/requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir --upgrade pip \
    && pip install --no-cache-dir -r /tmp/requirements.txt

# Calliope 0.7 goes in an isolated venv (its pins conflict with the above); the
# Go runner only calls the `calliope` CLI, so we expose just that on PATH.
COPY environment/requirements-calliope.txt /tmp/requirements-calliope.txt
RUN python -m venv /opt/calliope-venv \
    && /opt/calliope-venv/bin/pip install --no-cache-dir --upgrade pip \
    && /opt/calliope-venv/bin/pip install --no-cache-dir --pre -r /tmp/requirements-calliope.txt \
    && ln -s /opt/calliope-venv/bin/calliope /usr/local/bin/calliope

# Sanity-check that every framework and the solver import/resolve cleanly.
# (adopt_net0 doesn't expose __version__, so read it from package metadata.)
RUN python -c "import importlib.metadata as m; import pypsa, adopt_net0, highspy; \
print('pypsa', pypsa.__version__); \
print('adopt_net0', m.version('adopt_net0')); \
print('highspy OK')" \
    && calliope --version

# ---- Release binary ----
FROM go AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/meme ./cmd/meme

# ---- Production runtime (self-contained: no bind mount, no Go toolchain) ----
FROM frameworks AS prod
COPY --from=build /out/meme /usr/local/bin/meme
# Unprivileged user; HOME must be writable (matplotlib/pypsa caches).
RUN useradd --system --create-home --uid 10001 meme     && mkdir -p /data/work && chown -R meme:meme /data
USER meme
ENV HOME=/home/meme     PORT=8080     WORK=/data/work     EXEC=true
VOLUME ["/data/work"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3     CMD python -c "import os,urllib.request as u; u.urlopen('http://127.0.0.1:'+os.environ.get('PORT','8080')+'/healthz', timeout=3)"
# No .env file is baked in: configuration comes from the environment
# (PORT, WORK, EXEC, API_KEY, CORS_ORIGINS, SOLVER_TIMEOUT) or flags.
ENTRYPOINT ["meme"]

# ---- Development (default target; must stay last) ----
FROM frameworks AS dev
COPY --from=go /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:/root/go/bin:${PATH}"     GOPATH="/root/go"     GOTOOLCHAIN="local"
WORKDIR /src

# Pre-warm the Go module cache when a go.mod is present at build time.
COPY go.mod go.mod
RUN go mod download 2>/dev/null || true

# The rest of the source is bind-mounted by docker-compose at run time, so we
# don't COPY it here — that keeps the image reusable across code edits.

EXPOSE 8080

# Default: build then run the API in real-execution mode so /simulate drives
# the actual solvers. Uses the project Makefile so the container runs the same
# commands as a local checkout. Override the command in compose for `make test`.
CMD ["make", "run-exec"]
