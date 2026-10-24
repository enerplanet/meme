# API reference

All requests and responses are JSON; the result bundle is a zip. Every route
is also served under `/v1/` (`/v1/simulate`, …). Errors use one envelope:

```json
{ "error": "unknown target \"calliope-0.5\" (use one of [adopt-net0 calliope calliope-0.6 pypsa], a comma-separated list, or \"all\" for the defaults [adopt-net0 calliope pypsa])" }
```

| Method and path | Purpose | Success |
|---|---|---|
| `GET /healthz` | liveness and the build version | `200 {"status":"ok","version":"0.1.0-alpha"}` |
| `GET /capabilities` | targets, default targets, aliases, framework versions, feature matrix | `200` |
| `POST /validate?target=…` | validate a job for the target(s); nothing is written | `200` (`422` with the first rejection) |
| `POST /convert?target=…` | validate and emit the native model files synchronously | `200` with entrypoints |
| `POST /simulate?target=…` | validate, then schedule an asynchronous job that emits and runs each target | `202` with the job id |
| `GET /jobs/{id}/status` | job state, warnings, console log, per-run results | `200` |
| `GET /jobs/{id}` | the zipped result bundle, once the job is done | `200 application/zip` (`409` while running) |

## Selecting targets

`?target=` takes one name (`pypsa`), a comma-separated list
(`pypsa,calliope`) or `all`.

- `all` expands to the **default targets**, one per framework
  (`adopt-net0`, `calliope`, `pypsa`). A version variant such as
  `calliope-0.6` runs only when it is named, so `all` never runs two
  releases of the same framework unless you ask for both.
- A version-explicit **alias** resolves to its target: `calliope-0.7` is
  `calliope`. Duplicates collapse after resolution, order is preserved.
- An unknown name is a `400` listing every known name.

`GET /capabilities` is the discovery endpoint for all of this:

```json
{
  "targets":         ["adopt-net0", "calliope", "calliope-0.6", "pypsa"],
  "default_targets": ["adopt-net0", "calliope", "pypsa"],
  "aliases":         {"calliope-0.7": "calliope"},
  "versions":        {"adopt-net0": "0.1.10", "calliope": "0.7.0", "calliope-0.6": "0.6.8", "pypsa": "1.2.4"},
  "features":        {"pypsa": {"mode:plan": true, "mode:stochastic": true, "…": true}, "calliope": {"…": true}, "calliope-0.6": {"…": true}, "adopt-net0": {"…": true}}
}
```

## Authentication

When the server is started with `API_KEY` (or `-api-key`), every
payload-bearing request (`/validate`, `/convert`, `/simulate`) must carry the
key as a top-level `"api_key"` field in the JSON body; otherwise it is
rejected with `401 {"error": "invalid or missing api_key in payload"}`. The
key is compared in constant time, never logged, and **redacted from the
`config.json` persisted into the result bundle**. Without a configured key
the field is ignored. The `GET /jobs/{id}` routes carry no payload; they are
addressed by unguessable 128-bit random ids known only to the submitter.

## Validation and rejection

Validation runs in one pipeline for every target: inline series are interned
into the registry, the model and experiment are checked structurally, the
generic capability gates reject features the target does not claim, the
target's own gates reject what its emitter cannot represent faithfully, and
native blocks for other targets are reported as ignored. A `422` names the
first offending property; a `200`/`202` carries `warnings` for everything
that is honored approximately or dropped with notice.

```json
// POST /validate?target=calliope  (single target)
{ "valid": true, "warnings": ["solver \"highs\" is not usable with Calliope; emitting cbc instead"] }

// POST /validate?target=all  (one verdict per target; overall valid only when all are)
{ "valid": false, "targets": {
    "pypsa":      {"valid": true,  "warnings": []},
    "calliope":   {"valid": true,  "warnings": ["…"]},
    "adopt-net0": {"valid": false, "error": "multi-port conversion (flows) is not supported by target \"adopt-net0\""} } }
```

## Job lifecycle

```json
// POST /simulate?target=pypsa  → 202
{ "scheduled": true, "id": "…", "state": "queued", "target": "pypsa",
  "warnings": ["…"], "status_url": "/jobs/…/status", "result_url": "/jobs/…" }

// GET /jobs/{id}/status  (state: queued → running → succeeded | failed)
{ "id": "…", "target": "pypsa", "state": "running",
  "warnings": ["…"], "error": "", "log": "…full console log…",
  "runs": [ { "target": "pypsa", "index": 0, "input_dir": "…/run_0/input",
              "plan": {"target": "pypsa", "command": ["python", "run.py"], "work_dir": "…"},
              "assigned": {"technologies.pv.capacity.max": 400},
              "stdout": "…", "exit_code": 0, "error": "" } ],
  "created": "…", "started": "…", "finished": "…" }
```

A multi-target job (`"target": "adopt-net0,calliope,pypsa"`) runs every
framework in turn; a failing target does not stop the others, the job is
marked `failed` if any target failed, and the bundle still carries every
target that ran. Validation happens up front for every requested target, so
a hard rejection returns `422` at submit time, before any solver starts.
Jobs are kept in memory and garbage-collected 24 h after they finish.

## Result bundle

`GET /jobs/{id}` streams a zip:

```
metadata.json         job view (state, warnings, timestamps)
log.txt               the console log (write-through during the run)
results.json          per-run results (plan, exit code, stdout/stderr)
config.json           the submitted payload, byte-identical (api_key redacted)
files/run_0/run.py    the executed driver script (PyPSA, AdOpT)
files/run_0/input/…   the emitted native model
files/run_0/output/…  solver results (PyPSA network.nc; Calliope results.nc + csv/)
files/run_0/output/power_flow/…  PyPSA mode power_flow: convergence.csv, per-bus and per-branch tables, summary.json
files/run_1/…         further sweep points
```

For a multi-target job the trees nest per target: `files/<target>/run_i/…`.
AdOpT writes its results below its own input tree
(`files/<…>/run_0/input/results/`).

## Limits

- Request bodies are capped at 32 MiB (`413` beyond).
- Two jobs run concurrently; further submissions queue. Each solver run has
  a 10-minute budget and is killed, with its process group, on timeout or
  server shutdown.
- `?target=` must name a known target; the error lists them.

## Cross-origin browser access

CORS is off by default. `CORS_ORIGINS` (or `-cors-origins`) takes a
comma-separated allow list of exact origins, `*`, or subdomain wildcards
(`https://*.example.com`). Preflight `OPTIONS` is answered on every route
before authentication (a preflight carries no payload), the allowed origin is
echoed on responses, and `Content-Disposition` is exposed so browser code can
read the zip file name. Combining `*` with credentials is rejected at
startup, as are malformed origins. CORS is browser policy, not access
control; `API_KEY` remains the authentication.
