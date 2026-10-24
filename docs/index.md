![MEME banner](assets/logos/meme-banner-dark.png#only-dark)
![MEME banner](assets/logos/meme-banner-light.png#only-light)

# Multi Energy Model Execution

MEME is a dependency-free Go service (standard library only) that accepts one
**canonical JSON energy-system model** and translates it into the native input
formats of three optimization frameworks: [PyPSA](https://pypsa.org),
[Calliope](https://www.callio.pe) (release 0.7, with 0.6 as a selectable
version) and [AdOpT-NET0](https://github.com/UU-ER/AdOpT-NET0). It then
optionally runs the solve and returns a zipped result bundle. One request can
target a single framework, a subset, or all of them at once.

```
Client ──POST /simulate?target=pypsa,calliope──▶ MEME ── validate (capability gates)
       ◀──202 {id}────────────────────────────    │   ── emit native model files
Client ──GET /jobs/{id}/status──────────────────   ▼   ── run the solver (python / calliope CLI)
       ◀──state · log · per-run results────────  job ── bundle: results + inputs + config + log
Client ──GET /jobs/{id}───────────────────────▶ zip
```

## The core idea

Energy-system frameworks describe the same physics in different vocabularies:
a PyPSA `Generator` with `p_nom_max`, a Calliope `supply` tech with
`flow_cap_max`, an AdOpT database technology with a size bound. MEME defines
the shared concepts once, framework-neutrally, and translates. Where a
framework cannot represent a property the payload asks for, the request is
**rejected or the drop is named in a warning** — a payload is never accepted
and then partially ignored. Anything only one framework has travels in that
framework's `native` block, merged verbatim into its output.

```json
{
  "model": {
    "metadata": {"name": "two_nodes"},
    "time": {"start": "2025-01-01", "end": "2025-01-01T23:00", "resolution": "1H"},
    "carriers": {"electricity": {"unit": "MWh"}},
    "nodes": {"n1": {}, "n2": {}},
    "technologies": {
      "pv":   {"role": "supply", "node": "n1", "carrier_out": "electricity",
               "capacity": {"expandable": true, "max": 400}, "operation": {"max_pu": "pv_cf"},
               "costs": {"monetary": {"investment_per_capacity": 500000}}, "lifetime": 25, "interest_rate": 0.05},
      "load": {"role": "demand", "node": ["n1", "n2"], "carrier_in": "electricity", "demand_profile": 100}
    },
    "transmission": {"line": {"carrier": "electricity", "from": "n1", "to": "n2", "bidirectional": true,
                              "capacity": {"expandable": true, "max": 200}}},
    "timeseries": {"pv_cf": {"source": "inline", "values": [0, 0, 0.1, 0.4, 0.6, 0.6, 0.4, 0.1, 0, 0, 0, 0,
                                                            0, 0, 0.1, 0.4, 0.6, 0.6, 0.4, 0.1, 0, 0, 0, 0]}}
  },
  "experiment": {"mode": "plan", "solver": {"name": "highs"}}
}
```

The same payload becomes a PyPSA CSV folder with one `Load` per node, a
Calliope `model.yaml` with a data table carrying one column per node, or an
AdOpT `input_data/` tree — and solves to the same optimum on each.

## Highlights

- **Three frameworks, one contract.** PyPSA 1.2.4, Calliope 0.7.0 and
  AdOpT-NET0 0.1.10, selected with `?target=`; `all` runs every default
  framework in one job and bundles the results per target.
- **Framework versions as targets.** Calliope 0.6.8 is the `calliope-0.6`
  target with its own emitter and capability profile; `calliope-0.7` is the
  version-explicit alias of the default. `GET /capabilities` tells you which
  release each target drives.
- **Power flow as a run mode.** `experiment.mode: power_flow` makes PyPSA
  solve the AC network described in the canonical model (`electrical`
  fields, a fixed `dispatch`, reactive power and voltage control, shunts,
  controlled HVDC transfers) instead of optimizing: bus voltages, branch
  loading and convergence per snapshot. The consumer plans first, on any
  target, and calls again with the same payload; MEME never chains jobs.
- **PyPSA planning, complete.** The voltage law inside the optimization with
  line expansion and security constraints, energy-sized storage as a Store,
  integer modules, continuous retirement, priced slack demand, copperplate,
  the unit-commitment extras, a quadratic cost and investment periods, all
  through canonical fields; every optimization run returns its tables under
  `output/results/`.
- **Capability-gated validation.** Every feature claim is backed by a
  runnable scenario; a payload that asks for something the selected target
  cannot honor is rejected with a pointed message, never silently trimmed.
- **Async job API.** `POST /simulate` answers `202` with an id; poll
  `GET /jobs/{id}/status` for state and the live solver log, download the
  bundle from `GET /jobs/{id}`. Parameter sweeps expand into independent runs.
- **Native passthrough.** Anything a single framework owns (framework-only
  attributes, custom math, solver heuristics) rides in `native.<target>` and
  lands verbatim in that framework's files.
- **Verified translations.** Golden files freeze every emitted model byte for
  byte, a validation matrix freezes every accept/reject verdict, and a
  real-solver E2E suite pins objective values across frameworks and across
  Calliope releases.

## Where to go next

- [Quickstart](getting-started/quickstart.md): dry-run on the host, real
  solvers in Docker, first requests.
- [API reference](api.md): endpoints, the `target` parameter, job states,
  the result bundle.
- [Canonical model](model.md): the payload, the `Value` union, emitter
  outputs.
- [Targets and versions](targets.md) and the
  [capability reference](reference/capability-reference.md): what each
  framework honors, property by property.
- [Architecture](architecture.md) and the
  [architecture decisions](decisions/README.md): why the service looks the
  way it does.
