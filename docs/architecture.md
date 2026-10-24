# Architecture


## System architecture

```mermaid
flowchart TD
  Client["REST client"]

  subgraph Service["energymodel · Go service (stdlib-only)"]
    API["HTTP API<br/>(internal/api)"]
    REG["target registry<br/>(internal/target)"]
    subgraph TGT["target packages — self-contained per framework"]
      PY["target/pypsa<br/>caps · gates · CSV emitter · run.py"]
      CA["target/calliope<br/>0.7: model.yaml + math · 0.6: model/run/techs dialect<br/>caps · gates · CLI plans"]
      AD["target/adoptnet0<br/>caps · gates · input_data tree · run.py"]
    end
    KIT[["emit toolkit<br/>(internal/emit)"]]
    FILES["native files + solver results"]
    STORE[("JobStore + GC<br/>(service/jobs.go)")]
    EXEC["Executor (semaphore)<br/>+ Orchestrator<br/>(internal/service)"]
    RUN[["Runner: dry / command<br/>(ctx + timeout)"]]
  end

  SIM["Python simulators<br/>pypsa · calliope (0.7) · calliope-0.6 · adopt"]

  Client -->|"POST /convert, /validate"| API
  Client -->|"POST /simulate?target=…"| API
  Client -->|"GET /jobs/{id}[/status]"| API

  API --> REG
  REG -->|"generic gating + hooks"| TGT
  PY & CA & AD -. build on .-> KIT
  PY & CA & AD --> FILES

  API -->|"submit → 202 {id}"| STORE
  API -->|"go Execute(jobCtx)"| EXEC
  EXEC --> REG
  EXEC --> RUN
  RUN -->|"exec (killed on shutdown/timeout)"| SIM
  EXEC -->|results| STORE
  STORE -->|log.txt write-through| FILES
  API -->|read / zip bundle| STORE
```

**Colour of responsibility.** Everything inside `Service` is deterministic Go.
Each **target package** is a self-contained plugin (capability profile,
validation gates, emitter, run plan) registered with the **target registry**,
which owns the generic capability-driven validation. The **emit toolkit** is
the shared file-writing vocabulary the targets build on. The **Executor**
bounds solver concurrency and threads the server-lifetime context into every
solver process; the **JobStore** is the only shared mutable state (logs
write-through to disk, finished jobs are garbage-collected). The **Python
simulators** are the only external processes.


## Component catalog

| Package / file | Component | Responsibility |
|---|---|---|
| `internal/model` | canonical structs, `Model.Validate`, `Value`, constraint IR, `Performance`, electrical fields (`NodeElectrical`, `TransmissionElectrical`, `Technology.Dispatch`, `PowerFlowOptions`), `Native`, `Feature` | The framework-neutral vocabulary: JSON schema, structural + referential validation, shared feature names. Knows no target specifics. |
| `internal/emit` | `WriteCSV`, `WriteJSON`, `Ftoa`, `ValScalar`/`ScalarOr`/`OptFloat`, `AnnualizedCapex`, series helpers, native merge | Shared emission toolkit the target packages build on (CRF math, deterministic writers, native passthrough merging). |
| `internal/target` | `Target` interface (`Name`/`Version`/…), `Variant`, `Register`/`RegisterAlias`/`For`/`Resolve`/`Names`/`Defaults`/`Versions`, `ValidateFor`, `CapabilityMatrix`, `RunPlan` | The registry: resolves targets (and version aliases), separates default targets from version variants (`all` = defaults), aggregates capability profiles and framework versions, and orchestrates validation (structural → generic capability gates → per-target hooks → native warnings). |
| `internal/scripts` | `PyPSARun`, `AdOptNET0Run` | The Python driver scripts (`pypsa_run.py`, `adoptnet0_run.py`), embedded and exported for the target packages (go:embed cannot cross package directories). |
| `internal/target/pypsa` | `PyPSA`, `Emitter`, `MaterializeTimeSeries`, `ScenarioSets`, `powerflow.go` | Everything PyPSA: capability profile, mode preconditions, CSV-folder emitter + time-varying materialization, scoped-constraint projection, stochastic translation, the power-flow network (AC buses, lines, transformers, fixed `p_set`, slack control) and its gates, generated run.py driver (body from `internal/scripts/pypsa_run.py`; its `power_flow` branch runs `lpf()`/`pf()` and writes the result tables). |
| `internal/target/calliope` | `Calliope` (0.7), `Calliope06` (0.6), `Emitter`, `Emitter06`, math builders, YAML writer | Both Calliope releases. 0.7 (`calliope`, alias `calliope-0.7`): capability profile, MILP/piecewise/series gates, model.yaml + data tables + extra math (ratio pinning, piecewise, MILP), `calliope` CLI plan. 0.6 (`calliope-0.6`, a version variant): its own profile (k-means typical days and a native emissions objective gained; indexed params / piecewise / custom math absent) and gates, the model/run/techs/locations/links emitter with `file=` timeseries CSVs, conversion_plus carrier tiers and group_constraints, `calliope-0.6` CLI plan. Shared dependency-free YAML writer (fuzz-tested) and native merge (flat for 0.7, deep for 0.6's nested mappings). |
| `internal/target/adoptnet0` | `AdOptNET0`, `Emitter`, `dbTech` | Everything AdOpT-NET0: capability profile, database-mapping gate, input_data tree emitter with node-keyed overrides, generated driver (body from `internal/scripts/adoptnet0_run.py`). |
| `internal/target/all` | blank imports | Registers the built-in target packages (imported by `internal/api`); the calliope package registers both of its releases. |
| `internal/scenarios` | `Names`/`Raw`/`Load`, `Sample`/`SampleFor` | The embedded scenario corpus + per-target sample fixtures, importable from any test without relative paths. |
| `internal/service` | `Executor`, `Orchestrator`, `Runner`/`DryRunner`/`CommandRunner`, `JobStore`+GC, `WriteBundle` | Job execution: bounded concurrency, sweep expansion, per-target run loop, context/timeout-enforced solver processes, on-disk job logs, TTL garbage collection, result-bundle layout. |
| `internal/api` | `Server`, `NewServer`, handlers, JSON error envelope | HTTP adapter only: multi-target `?target=` resolution, size-capped decoding, `api_key` auth (constant-time; 401 envelope; redacted from the bundle), `/v1` aliasing, streaming the bundle. |
| `internal/env`, `internal/cli` | `Load`, `Config`, `Parse` | Dependency-free `.env` reader; CLI + config precedence **CLI flag > .env > default**. |
| `cmd/meme` | `main` | Wiring + lifecycle: solver timeout, job concurrency, job-TTL GC, `ReadHeaderTimeout`, signal-driven graceful shutdown that kills running solvers. |


## Data flow & processing logic

### Two request styles

- **Synchronous** (`/convert`, `/validate`, `/capabilities`): decode → validate →
  (emit) → respond in the same request.
- **Asynchronous** (`/simulate`): validate synchronously (so hard errors return
  immediately), schedule a background job, return a job id; the caller polls.

`?target=` on all three POST endpoints takes a single target, a comma-separated
list, or `all`. Multi-target requests validate for **every** requested target
up front (422 naming the target if any rejects — a job never silently drops one
of its frameworks) and then run the targets sequentially; a target failing at
run time doesn't stop the rest, but marks the job `failed`. Single-target
requests keep the response shapes and bundle layout shown below; multi-target
responses carry per-target verdicts/entrypoints and nest the bundle under
`files/<target>/`.

### Async job lifecycle

```mermaid
sequenceDiagram
  actor C as Client
  participant A as API (server.go)
  participant S as JobStore
  participant W as Worker + Orchestrator
  participant R as Runner
  participant P as Python simulator

  C->>A: POST /simulate?target= (Job JSON)
  A->>A: ValidateFor(target)
  alt hard validation error
    A-->>C: 422 {scheduled:false, error, warnings}
  else valid
    A->>S: create JobRecord (queued)
    A-->>C: 202 {id, warnings, status_url, result_url}
    A->>W: go runJob()
    W->>S: state = running
    W->>W: ExpandRuns (sweep → N jobs)
    loop each run
      W->>W: emit native files
      W->>W: materialize time series (PyPSA)
      W->>R: Run(plan)
      R->>P: exec command (CommandRunner)
      P-->>R: stdout / exit code
      R-->>W: RunResult
      W->>S: append console log
    end
    W->>S: state = succeeded/failed + results
  end

  C->>A: GET /jobs/{id}/status
  A->>S: read snapshot
  A-->>C: {state, log, runs, error}

  C->>A: GET /jobs/{id}
  A->>S: read (must be done)
  alt not finished
    A-->>C: 409 {state, message}
  else finished
    A-->>C: 200 application/zip (metadata + log + results + files)
  end
```

### Emit pipeline (the translator)

```mermaid
flowchart LR
  J["Job"] --> V["target.ValidateFor(job, name)"]
  V --> E{"target.For(name).Emit"}
  E --> T["Transforms<br/>• node×carrier → buses (PyPSA)<br/>• overnight capex → CRF annualized<br/>• at(node): per-node overrides<br/>• flows → multi-output link<br/>• Value.Reduce (indexed→scalar)<br/>• constraints → global/math/sidecar<br/>• native.&lt;target&gt; merge"]
  T --> O["native input files<br/>+ JSON sidecars"]
```

**Shared transforms.**
- `busGrid` + `BusID` (`target/pypsa`) — PyPSA fuses `(node, carrier)` into
  buses; the emitter materializes exactly the pairs used.
- `emit.AnnualizedCapex` — converts overnight investment to a per-year cost via
  the capital recovery factor when the target expects it (PyPSA); both Calliope
  releases pass overnight through and annualize internally (0.7 via
  `cost_interest_rate` + `lifetime`, 0.6 via `interest_rate` + `lifetime`); an
  already-annualized basis is pinned to a depreciation rate of 1 so the yearly
  figure is charged unchanged.

### Orchestrator internals

```mermaid
flowchart TD
  Sim["Executor.Execute → Orchestrator.Simulate(ctx, job, target, dir)"] --> Exp["ExpandRuns: sweep → []Job<br/>(deep clone + applyPath)"]
  Exp --> Loop{"for each run"}
  Loop --> Emit["emitter.Emit → files"]
  Emit --> Mat["MaterializeTimeSeries<br/>snapshots.csv, loads-p_set.csv,<br/>generators-p_max_pu.csv"]
  Mat --> Plan["defaultPlan(target) → command"]
  Plan --> Run["Runner.Run(plan)"]
  Run --> Res["collect RunResult<br/>(stdout, exit, error)"]
  Res --> Loop
  Loop --> Done["[]RunResult + log"]
```

- **`ExpandRuns` / `applyPath`** — `experiment.sweep` becomes one deep-cloned Job
  per grid point; a dotted path (`technologies.pv.capacity.max`) is applied to
  each clone.
- **`MaterializeTimeSeries`** — builds the snapshot calendar from `model.time`
  and writes PyPSA's per-attribute time-varying CSVs from the inline series
  registry (this closes the emitter's static-only gap).
- **`Runner`** — `DryRunner` (default) records the planned command;
  `CommandRunner` shells out to the simulator and captures stdout/exit.



## Repository layout

```
.
├── cmd/meme/                server main: config wiring, solver timeouts, job GC,
│                            graceful shutdown (kills running solvers)
├── internal/
│   ├── api/                 HTTP transport: routing (+/v1 alias), multi-target
│   │                        resolution, api_key auth, JSON error envelope, zip download
│   ├── cli/                 flag/.env resolution (CLI > .env > default)
│   ├── env/                 dependency-free .env file parser
│   ├── model/               canonical schema + structural validation (framework-neutral)
│   ├── emit/                shared emission toolkit: CSV/JSON writers, CRF math,
│   │                        time-series helpers, native-block merging
│   ├── scripts/             Python driver scripts (pypsa_run.py, adoptnet0_run.py),
│   │                        embedded and exported for the target plugins
│   ├── target/              target registry + capability-driven validation pipeline
│   │   ├── pypsa/           plugin: gates, CSV emitter, embedded run.py driver
│   │   ├── calliope/        plugins: Calliope 0.7 (model.yaml + custom math) and
│   │   │                    Calliope 0.6 (model/run/techs/locations dialect), CLI run plans
│   │   ├── adoptnet0/       plugin: gates, input_data tree, embedded run.py driver
│   │   ├── all/             blank-import umbrella that registers the plugin packages
│   │   └── testdata/golden/ emitter snapshots + frozen validation matrix
│   ├── scenarios/           embedded corpus: 49 runnable payloads + shared sample
│   └── service/             Executor (bounded concurrency), Orchestrator (sweep →
│                            emit → run), Runners (dry / command+timeout),
│                            JobStore + GC, result-zip bundling
├── test/                    test pyramid docs + Makefile; test/e2e/ = real-solver suite
├── environment/             Docker image, compose, per-env .env files, pinned
│                            framework requirements (one venv per Calliope release)
├── examples/                maximal + shared payloads with the curl walkthrough
├── schemas/                 the published JSON Schemas (revised_unified_schema.json
│                            = the payload contract, checked by `make schema-check`),
│                            the per-framework schemas, schema-vs-Go report
├── docs/                    this documentation site (MkDocs): guides, the capability
│                            reference, architecture decisions
├── Makefile                 build/run; delegates to test/ and environment/
└── .env.example             annotated configuration template
```

Each **target plugin** is self-contained (capability profile, validation
gates, emitter, run plan) and self-registers; adding a fourth framework means
one new package under `internal/target/` plus one blank import. A framework
may register several **versions** as separate targets: the bare name is the
default release, a `-<major.minor>` suffix pins another one (`calliope-0.6`),
and a version-explicit alias of the default (`calliope-0.7`) keeps a pinned
spelling valid when the default moves on. `?target=all` expands to the
defaults only, so one request never runs two releases of the same framework
unless it names them.

Ready-to-run example payloads live in [`examples/`](https://github.com/enerplanet/meme/tree/main/examples/): one **maximal**
config per framework (every supported property), plus `shared_full.json` and
`payload_full.json` — configs valid for **all three frameworks at once**, the
latter carrying pypsa+calliope+adopt native blocks side by side — together with
the full curl walkthrough (validate → convert → simulate → poll → zip,
single- and multi-target).

