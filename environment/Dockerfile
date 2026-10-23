# syntax=docker/dockerfile:1
#
# Build-and-test environment for the meme (energymodel) service.
#
# One image that carries everything needed end-to-end:
#   * Go 1.22 toolchain  -> build the stdlib-only service and run `go test`
#   * Python 3.12        -> the three frameworks the service emits for
#   * PyPSA + HiGHS, Calliope 0.7.0 and 0.6.8 + CBC, AdOpT-NET0 + GLPK
#     (all free solvers; each Calliope release in its own venv)
#
# With all of the above on PATH, the service can run in real-execution mode
# (`meme -exec`) so /simulate actually invokes the solvers, and integration
# tests can validate emitted files against pinned framework versions.

# ---- Go toolchain (copied into the final image) ----
FROM golang:1.22-bookworm AS go

# ---- Final image: Python base + Go toolchain + frameworks ----
# Python 3.12: AdOpT-NET0 (adopt_net0) requires >=3.12.
FROM python:3.12-slim-bookworm

# Bring the Go toolchain over from the golang image.
COPY --from=go /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:/root/go/bin:${PATH}" \
    GOPATH="/root/go" \
    GOTOOLCHAIN="local"

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

# Calliope: one isolated venv per supported release (their pins conflict with
# each other and with the system Python). The Go runner only calls the CLIs,
# so just those are exposed on PATH:
#   calliope      -> 0.7.0  (target "calliope", the default)
#   calliope-0.7  -> 0.7.0  (same binary; the version-explicit alias)
#   calliope-0.6  -> 0.6.8  (target "calliope-0.6")
# uv provisions the interpreters: the 0.7 venv reuses the system Python 3.12,
# while Calliope 0.6.8's pins have no 3.12 wheels, so its venv runs on a
# uv-managed Python 3.8 kept under /opt/uv-python (the system Python is
# untouched). Each venv is its own layer, so a pin bump rebuilds only it.
ENV UV_PYTHON_INSTALL_DIR=/opt/uv-python
RUN pip install --no-cache-dir uv

COPY environment/requirements-calliope-0.7.txt /tmp/requirements-calliope-0.7.txt
RUN uv venv --python /usr/local/bin/python3 /opt/calliope-0.7-venv \
    && uv pip install --python /opt/calliope-0.7-venv/bin/python --no-cache -r /tmp/requirements-calliope-0.7.txt \
    && ln -s /opt/calliope-0.7-venv/bin/calliope /usr/local/bin/calliope \
    && ln -s /opt/calliope-0.7-venv/bin/calliope /usr/local/bin/calliope-0.7

COPY environment/requirements-calliope-0.6.txt /tmp/requirements-calliope-0.6.txt
RUN uv venv --python 3.8 --python-preference only-managed /opt/calliope-0.6-venv \
    && uv pip install --python /opt/calliope-0.6-venv/bin/python --no-cache -r /tmp/requirements-calliope-0.6.txt \
    && ln -s /opt/calliope-0.6-venv/bin/calliope /usr/local/bin/calliope-0.6

# Sanity-check that every framework and the solver import/resolve cleanly.
# (adopt_net0 doesn't expose __version__, so read it from package metadata.)
RUN python -c "import importlib.metadata as m; import pypsa, adopt_net0, highspy; \
print('pypsa', pypsa.__version__); \
print('adopt_net0', m.version('adopt_net0')); \
print('highspy OK')" \
    && calliope --version && calliope-0.7 --version && calliope-0.6 --version

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
