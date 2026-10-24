# Examples

Six payloads — one **maximal** config per framework (every property that
target honors per the [capability reference](../reference/capability-reference.md)),
two **shared** configs valid for every framework at once, and a PyPSA
**power-flow** payload (the second call after a plan) — and the API calls to
drive them. All six have been executed against the real API with real solvers
and run to `succeeded` (PyPSA/HiGHS, Calliope/CBC, AdOpT/GLPK); the full E2E
tier re-runs them on every change.

| File | Target | Exercises |
|---|---|---|
| [`pypsa_full.json`](https://github.com/enerplanet/meme/blob/main/examples/pypsa_full.json) | `pypsa` | 3 carriers, 2 nodes (coords + native bus columns), physics PV with precomputed CF + per-node overrides, committable CCGT (full UC set + ramps + min/max_pu), time-varying marginal cost, multi-output CHP (rigid ratios), battery (cyclic, self-discharge, initial SOC), hydro (inflow series + spill cost, non-cyclic), bidirectional line with native `x`/`r`, trade with **series** import price + emission factor + export revenue, CO₂ cap (GlobalConstraint), one unscoped + two scoped/multi-term constraints (run.py sidecar), a **series import limit** (fixed-p_nom generator + time-varying `p_max_pu`), per-distance line losses folded into the link efficiency, snapshot **weights**, `build_year`/`annual_output_max`/`systemwide_max`, typed solver `time_limit`/`threads`, tech/carrier/system native blocks, and a 2-point **sweep** → 2 independent runs |
| [`pypsa_power_flow.json`](https://github.com/enerplanet/meme/blob/main/examples/pypsa_power_flow.json) | `pypsa` | **Mode `power_flow`**: no optimization — an HV slack feeding two 20 kV nodes through a transformer and a line (`node.electrical.v_nom`, `transmission.electrical`), PV and a battery following their `dispatch` series, two loads; Newton-Raphson seeded by a linear solve, results under `output/power_flow/` (per-bus voltages, line and transformer loading, `summary.json`). The second call of a plan-then-check workflow; see below |
| [`pypsa_power_flow_minimal.json`](https://github.com/enerplanet/meme/blob/main/examples/pypsa_power_flow_minimal.json) | `pypsa` | The **smallest valid power flow**: two 20 kV nodes, one line whose rating defaults to its installed capacity, a slack generator, a load with an inline two-snapshot profile, and nothing else — the shape to start from |
| [`pypsa_power_flow_meshed.json`](https://github.com/enerplanet/meme/blob/main/examples/pypsa_power_flow_meshed.json) | `pypsa` | A **meshed** 20 kV ring under an HV transformer: one PV technology placed at two nodes with a `dispatch` series per node (`node_overrides`), one load technology at three nodes with a per-node profile, two parallel transformers (`num_parallel`, `tap_ratio`), the slack spelled `grid@hv`, a tight `tolerance`; real loop flows and a visible voltage sag |
| [`pypsa_power_flow_options.json`](https://github.com/enerplanet/meme/blob/main/examples/pypsa_power_flow_options.json) | `pypsa` | The **options**: `method: linear` (DC, never diverges, voltages stay nominal), `distribute_slack` with the slack carrying its own expected dispatch, a battery whose dispatch changes sign (positive = discharge), and an electric boiler feeding a `heat` carrier the power flow carries as a fixed injection without balancing it (warned) |
| [`pypsa_power_flow_reactive.json`](https://github.com/enerplanet/meme/blob/main/examples/pypsa_power_flow_reactive.json) | `pypsa` | **Reactive power and voltage control**: the slack holds 1.02 pu, a `pv` generator holds its bus at 1.0 pu by supplying reactive power, loads carry a `power_factor` or a `reactive_dispatch` series, a `shunt` compensates at one node, a controlled HVDC link (`transmission.dispatch`) feeds an island PyPSA balances on its own generator, a line given per km (`r_per_length`, `x_per_length`, `b`) and a phase-shifting transformer |
| [`pypsa_planning.json`](https://github.com/enerplanet/meme/blob/main/examples/pypsa_planning.json) | `pypsa` | **Planning on the electrical network**: a 110 kV ring solved under the voltage law (`network.model: linear_power_flow`) with an expandable, priced line (`lifetime`, `interest_rate`, `max_loading`), a generator built in 10 MW modules (`per_unit`), a retirable old unit (`decommission: continuous`), a committable unit with start/stop ramps, standby cost and prior uptime, an energy-sized battery as a Store with independent rates and a depth of discharge, and priced slack for unmet demand; read `results/capacities.csv`, `lines-p0.csv` and `statistics.csv` |
| [`calliope_full.json`](https://github.com/enerplanet/meme/blob/main/examples/calliope_full.json) | `calliope` | availability data table from a CF series, first-class `source` + `area` (per-area resource on a bounded area), **MILP committable** (integer 50-MW units + purchase cost + min stable load), multi-output CHP via **ratio-pinning math**, **piecewise** part-load conversion (fixed 300 MW), battery with energy-capacity costs (`cost_storage_cap`) + `cost_om_annual` + **depth of discharge**, transmission (bidirectional, distance, **per-distance investment cost**), `reporting.save_logs`, typed solver `time_limit`, trade (import price + co2 factor, paid export), CO₂ cap + scoped constraint (custom math), native `flow_out_parasitic_eff`, and **resample 1H → 3H** |
| [`adopt_full.json`](https://github.com/enerplanet/meme/blob/main/examples/adopt_full.json) | `adopt-net0` | full climate block (ghi/dni/dhi/temp_air/rh/ws10; series and scalars mixed), three physics techs (`pv` → Photovoltaic, `wind` → WindTurbine, `heat_pump` → HeatPump_AirSourced serving a heat demand), battery on two nodes with a **per-node override** (`size_max` 100 vs 40), tech-level native patch, trade at both nodes with a **series** import price + import/**export** emission factors, `reporting.case_name`, typed solver `time_limit`/`mip_gap`, and a CO₂ emission cap (`costs_emissionlimit` objective) |
| [`shared_full.json`](https://github.com/enerplanet/meme/blob/main/examples/shared_full.json) | **`all`** | The maximal **intersection**: one config valid for all three frameworks *simultaneously* — physics PV + wind carrying both a climate block (AdOpT computes output itself) and precomputed CF profiles (PyPSA/Calliope), a two-node battery with a per-node override, scalar-priced trade at both nodes (+ import co2 factor), an emission cap, a battery-capacity **sweep**, a global **`discount_rate`** and typed solver `time_limit` (both honored by all three), and a per-target `native` block on the same tech (each framework picks only its own). Submitted once with `?target=all` → **6 solves** (3 frameworks × 2 sweep points) in one job, one zip |
| [`payload_full.json`](https://github.com/enerplanet/meme/blob/main/examples/payload_full.json) | **`all`** | `shared_full` pushed to the limit, with **all native scopes from the three per-target payloads riding together**: three physics techs (PV, wind, heat pump — each with climate data for AdOpT *and* a precomputed profile for PyPSA/Calliope), two carriers, capacity `min`, and native blocks at every honored scope — carrier (`nice_name`), node (`v_nom`), model (`_native.pypsa.json`), and a battery carrying `pypsa` + `calliope` + `adopt-net0` fragments side by side. Each framework consumes only its own key; the validate response's warnings read like a routing table of who ignored what. Verified: `?target=all` → 6/6 optimal solves |

The three per-target payloads are deliberately *not* portable — and a raw
*union* of them cannot validate anywhere: `pypsa_full`'s UC extras are rejected
by Calliope, `calliope_full`'s piecewise tech by PyPSA, the CHP/constraints/
transmission by AdOpT (see [Targets and versions](../targets.md)). `payload_full.json` is the honest maximum of
"one config, three frameworks": the largest shared property set, with each
framework's extras isolated in its own `native` namespace.

## 0. Start the API (real solvers)

```bash
make -C environment run ENV=dev  # API on :8080 with PyPSA + Calliope + AdOpT + solvers
```

Every payload below already carries a top-level `"api_key": "s3cret"`. It is
required only when the server was started with a matching `API_KEY`; when no key
is configured (the default) the field is ignored, so the calls work as written
either way. For a server using a different key, edit the field:

```bash
curl -s -X POST 'localhost:8080/validate?target=pypsa' \
     -d "$(jq '.api_key="yourkey"' examples/pypsa_full.json)"
```

## 1. Discover capabilities

```bash
curl -s localhost:8080/capabilities | jq .
# -> {"targets":["adopt-net0","calliope","calliope-0.6","pypsa"],   <- every selectable target
#     "default_targets":["adopt-net0","calliope","pypsa"],           <- what ?target=all runs
#     "aliases":{"calliope-0.7":"calliope"},                           <- version-explicit spellings
#     "versions":{"calliope":"0.7.0","calliope-0.6":"0.6.8","pypsa":"1.2.4","adopt-net0":"0.1.10"},
#     "features":{"pypsa":{"mode:stochastic":true,…},"calliope-0.6":{…},…}}
```

Calliope is available in two releases: `calliope` (0.7.0, also spelled
`calliope-0.7`) and `calliope-0.6` (0.6.8, the legacy `model`/`run`/`techs`/
`locations` dialect). The 0.6 target runs only when named — `all` means one
run per framework at its default version:

```bash
curl -s -X POST 'localhost:8080/validate?target=calliope-0.6' -d @examples/shared_full.json | jq .
curl -s -X POST 'localhost:8080/convert?target=calliope,calliope-0.6' -d @examples/shared_full.json | jq .entrypoints
# -> {"calliope": ".../calliope/input/model.yaml", "calliope-0.6": ".../calliope-0.6/input/model.yaml"}
```

## 2. Validate (fast, no files written)

```bash
curl -s -X POST 'localhost:8080/validate?target=pypsa'      -d @examples/pypsa_full.json    | jq .
curl -s -X POST 'localhost:8080/validate?target=calliope'   -d @examples/calliope_full.json | jq .
curl -s -X POST 'localhost:8080/validate?target=adopt-net0' -d @examples/adopt_full.json    | jq .
# -> {"valid":true, "warnings":[…]}   (a 422 names exactly the property the target cannot honor)
```

## 3. Convert only (emit the native model to disk, don't solve)

```bash
curl -s -X POST 'localhost:8080/convert?target=calliope' -d @examples/calliope_full.json | jq .
# -> {"target":"calliope", "entrypoint":"<workdir>/…/input/model.yaml", "warnings":[…]}
```

## 4. Simulate (async): submit → poll → download the bundle

```bash
# submit (returns 202 immediately; hard validation errors return 422 here)
ID=$(curl -s -X POST 'localhost:8080/simulate?target=pypsa' \
     -d @examples/pypsa_full.json | jq -r .id)

# poll status (state: queued -> running -> succeeded|failed; includes live log)
curl -s "localhost:8080/jobs/$ID/status" | jq '{state, warnings, runs: [.runs[]?.exit_code]}'

# fetch the result bundle once finished
curl -s -o result.zip "localhost:8080/jobs/$ID"
unzip -l result.zip
#   metadata.json  log.txt  results.json  config.json     <- initial config (api_key stripped)
#   files/run_0/run.py                                    <- the executed driver
#   files/run_0/input/…                                   <- emitted native model
#   files/run_0/output/network.nc                         <- solved network (PyPSA)
#   files/run_1/…                                         <- second sweep point
```

Per-target solver results inside the zip: PyPSA `files/run_i/output/network.nc`
(+ `network_baseline.nc` in MGA mode); Calliope `files/run_i/output/results.nc`
+ `output/csv/*`; AdOpT `files/run_i/input/results/` (HDF5 + `Summary.xlsx` +
solver log).

## 5. One config, all three frameworks at once

`?target=` takes a single name, a comma-separated list, or `all`. One submit
emits, executes and solves every requested framework; the bundle nests each
target's tree under `files/<target>/`.

```bash
# per-target verdicts in one call
curl -s -X POST 'localhost:8080/validate?target=all' -d @examples/shared_full.json | jq .
# {"valid":true, "targets":{"pypsa":{"valid":true,"warnings":[…]},
#                            "calliope":{…}, "adopt-net0":{…}}}

# emit all three native models without solving
curl -s -X POST 'localhost:8080/convert?target=pypsa,calliope,adopt-net0' \
     -d @examples/shared_full.json | jq .entrypoints

# solve all three in one job (here: 3 frameworks x 2 sweep points = 6 solves)
ID=$(curl -s -X POST 'localhost:8080/simulate?target=all' \
     -d @examples/shared_full.json | jq -r .id)
curl -s "localhost:8080/jobs/$ID/status" | jq '{state, runs: [.runs[]? | {target, index, exit_code}]}'

curl -s -o shared.zip "localhost:8080/jobs/$ID"
unzip -l shared.zip
#   config.json                                        <- the one shared config
#   files/pypsa/run_0/output/network.nc                <- + run_1 (sweep)
#   files/calliope/run_0/output/results.nc             <- + csv/, run_1
#   files/adopt-net0/run_0/input/results/…             <- HDF5 + Summary.xlsx, run_1
```

If any requested target rejects the payload, the whole submit returns 422
naming the target and the reason — a multi-target job never silently drops one
of its frameworks. At run time a failing target does not stop the others; the
job is marked `failed` but the bundle still carries every target that ran.

## 6. Run-mode variants

Modes are mutually exclusive per experiment, so the maximal payloads use
`plan`; every other mode has a ready-to-run scenario in
[`internal/scenarios/testdata/scenarios/`](https://github.com/enerplanet/meme/tree/main/internal/scenarios/testdata/scenarios/):

```bash
S=internal/scenarios/testdata/scenarios
curl -s -X POST 'localhost:8080/simulate?target=pypsa'      -d @$S/pypsa_operate.json       # rolling horizon
curl -s -X POST 'localhost:8080/simulate?target=pypsa'      -d @$S/pypsa_alternatives.json  # MGA
curl -s -X POST 'localhost:8080/simulate?target=pypsa'      -d @$S/pypsa_stochastic.json    # two-stage stochastic
curl -s -X POST 'localhost:8080/simulate?target=calliope'   -d @$S/calliope_operate.json    # receding horizon
curl -s -X POST 'localhost:8080/simulate?target=calliope'   -d @$S/calliope_spores.json     # SPORES
curl -s -X POST 'localhost:8080/simulate?target=adopt-net0' -d @$S/adopt_pareto.json        # Pareto front
curl -s -X POST 'localhost:8080/simulate?target=adopt-net0' -d @$S/adopt_monte_carlo.json   # Monte-Carlo
curl -s -X POST 'localhost:8080/simulate?target=adopt-net0' -d @$S/adopt_typicaldays.json   # typical days
```

## 7. Seeing a rejection (the gates at work)

```bash
# PyPSA payload sent to AdOpT -> 422 naming the first unsupported feature
curl -s -X POST 'localhost:8080/validate?target=adopt-net0' -d @examples/pypsa_full.json | jq .
# {"valid":false, "error":"multi-port conversion (flows) is not supported by target \"adopt-net0\""}
# (drop the CHP and the next gate fires: tech "ccgt" has no adopt_net0 database mapping …)
```

## Power flow: the second call after a plan

A power flow is not an optimization, and MEME does not chain jobs: the API
consumer plans first (on Calliope, or on PyPSA), reads the dispatch out of
that result, and submits a second payload in mode `power_flow` to `pypsa`
(see [ADR-0008](../decisions/0008-power-flow-as-a-pypsa-run-mode.md)). The
second payload carries the electrical network (`electrical.v_nom` on every
node of it, `electrical` on every arc of it), a `dispatch` series on every
supply, conversion and storage technology except the one named as `slack`,
and fixed capacities throughout.

```bash
# 1. plan as usual (any target) and read the dispatch out of the bundle
ID=$(curl -s -X POST 'localhost:8080/simulate?target=calliope' -d @plan.json | jq -r .id)
curl -s -o plan.zip "localhost:8080/jobs/$ID"      # results/csv/flow_out.csv -> per-tech dispatch

# 2. power flow of that dispatch on pypsa
ID=$(curl -s -X POST 'localhost:8080/simulate?target=pypsa' \
     -d @examples/pypsa_power_flow.json | jq -r .id)
curl -s "localhost:8080/jobs/$ID/status" | jq '{state, log: (.log | split("\n") | map(select(test("power flow"))))}'
# -> "power flow converged 4/4 snapshots", "status ok power flow converged"
curl -s -o pf.zip "localhost:8080/jobs/$ID"
unzip -p pf.zip files/run_0/output/power_flow/summary.json
#   {"method":"newton_raphson","slack":"grid","slack_bus":"hv::electricity","snapshots":4,
#    "converged_snapshots":4,"min_v_mag_pu":0.975,"max_v_mag_pu":1.003,
#    "max_line_loading":0.306,"max_transformer_loading":0.428,"failure":null}
```

A snapshot that does not converge fails the run (exit code 3, job `failed`)
with `convergence.csv` and the tables still written for diagnosis; the
`linear` method (`experiment.power_flow.method`) always solves and keeps the
voltages nominal. Calliope and AdOpT reject the mode outright; a `dispatch`
in any optimization mode is rejected on every target with a pointer to
`operation.equals_pu`, so a planning payload and a power-flow payload cannot
be confused.

### The power-flow payloads

Five payloads cover the mode; start from the minimal one and grow it:

```bash
for f in pypsa_power_flow_minimal pypsa_power_flow pypsa_power_flow_meshed pypsa_power_flow_options pypsa_power_flow_reactive; do
  ID=$(curl -s -X POST 'localhost:8080/simulate?target=pypsa' -d @examples/$f.json | jq -r .id)
  until curl -s "localhost:8080/jobs/$ID/status" | jq -e '.state | test("succeeded|failed")' >/dev/null; do sleep 1; done
  curl -s -o $f.zip "localhost:8080/jobs/$ID" && unzip -p $f.zip files/run_0/output/power_flow/summary.json | jq -c '{method, converged_snapshots, min_v_mag_pu, max_line_loading}'
done
```

| Payload | Shows |
|---|---|
| `pypsa_power_flow_minimal.json` | the three ingredients and nothing else: `electrical.v_nom` on both nodes, `electrical` on the one line (rating defaulting to `capacity.existing`), a `slack`; the load profile is an inline array |
| `pypsa_power_flow.json` | an HV/MV network with a transformer and a line, a PV `dispatch` series, a battery following its own series |
| `pypsa_power_flow_meshed.json` | loop flows in a meshed ring, per-node `dispatch` and `demand_profile` through `node_overrides`, `num_parallel` and `tap_ratio` on the transformer, the slack as `tech@node`, a tight `tolerance` |
| `pypsa_power_flow_options.json` | `method: linear`, `distribute_slack`, a storage dispatch that changes sign, a conversion into a second carrier |
| `pypsa_power_flow_reactive.json` | reactive power two ways, `pv` voltage control and a slack set-point, a shunt, a controlled HVDC link into an island, per-length impedance, phase shift |

### The options

- **`method`** — `newton_raphson` (default) runs a linear solve first and the
  full AC solution from that seed, so voltage magnitudes, angles and reactive
  flows come out; `linear` stops after the DC solve: active flows and angles
  only, every voltage stays at 1.0 pu, and it cannot diverge.
- **`slack`** — required; the supply technology whose generator takes up the
  balance (losses included). Spell it `tech`, or `tech@node` when the
  technology is placed at several nodes. Every other supply, storage and
  conversion technology needs a `dispatch`.
- **`distribute_slack`** — PyPSA then shares the balance over all generators
  in proportion to their dispatch. The slack therefore needs a `dispatch` too
  (its expected output); without one the request is rejected, because PyPSA
  would move the whole balance onto the other generators.
- **`tolerance`** — the Newton-Raphson convergence tolerance in pu (default
  `1e-6`); irrelevant for `linear`.
- **Sign conventions of `dispatch`** — supply: output; conversion: input-side
  flow (`p_set` on the Link's `bus0`); storage: discharge, so a negative value
  charges. Demand keeps `demand_profile`.
- **Ratings** — `electrical.s_nom` (MVA) defaults to the arc's
  `capacity.existing`; the loading tables divide by it. `num_parallel`
  scales the impedance of parallel circuits; `tap_ratio` applies to
  transformers only.
- **Other carriers** — a power flow balances the electricity network only.
  Components on other carriers (a heat demand, an electric boiler's output)
  are carried as fixed injections and reported in a warning.
- **Reactive power** (`technology.electrical`) — either `reactive_dispatch`
  in MVAr per timestep, or a `power_factor` from which Q is derived per
  snapshot in the component's own direction (a load consumes, a generator
  supplies; `leading: true` flips the sign). One or the other, on supply,
  demand and storage; a conversion transfers active power only.
- **Voltage control** — `control: pv` with a `voltage_setpoint` (pu) makes a
  generator hold its bus voltage by supplying or absorbing reactive power;
  the slack may carry a `voltage_setpoint` as well (default 1.0). All
  set-points at one bus must agree. `method: linear` ignores all of this and
  says so.
- **Shunts** (`node.electrical.shunt`) — a fixed admittance at the bus in
  siemens; `b` positive is capacitive.
- **Controlled transfers** — an electricity arc with a `dispatch` and no
  `electrical` block is a PyPSA Link with a fixed `p_set`: the model of an
  HVDC tie. It does not join the two buses electrically, so the part of the
  network behind it is its own AC island, which needs a generator; PyPSA
  balances that island on its largest generator and the validation says so.
- **Branch refinements** — `r_per_length` and `x_per_length` in ohm/km
  instead of `r` and `x`, multiplied by `distance`; line shunt admittance
  `b`, `g`; transformer `phase_shift` in degrees.

### What comes back

`files/run_0/output/power_flow/` in the bundle, next to `network.nc`:

| File | Content |
|---|---|
| `summary.json` | `method`, `slack`, `slack_bus`, `snapshots`, `converged_snapshots`, `min_v_mag_pu`, `max_v_mag_pu`, `max_line_loading`, `max_transformer_loading` (figures over converged snapshots; `null` when none converged), `failure` |
| `convergence.csv` | per snapshot: `converged`, `iterations` (Newton-Raphson only) |
| `buses-v_mag_pu.csv`, `buses-v_ang.csv`, `buses-p.csv`, `buses-q.csv` | per bus (`node::carrier`) and snapshot; `q` only for Newton-Raphson |
| `lines-p0.csv`, `lines-p1.csv`, `lines-q0.csv`, `lines-q1.csv`, `lines-loading.csv` | per line: flow at each end, apparent loading over `s_nom` |
| `transformers-…` | the same per transformer |
| `generators-p.csv`, `generators-q.csv`, `loads-p.csv`, `loads-q.csv`, `storage_units-p.csv`, `storage_units-q.csv`, `links-p0.csv`, `links-p1.csv`, `shunt_impedances-p.csv`, `shunt_impedances-q.csv` | the injections as solved (the slack absorbs the losses; `q` tables for Newton-Raphson only) |

The job log carries `power flow converged k/n snapshots` and
`status ok power flow converged`; a snapshot that does not converge makes the
run exit 3 and the job `failed`, with `convergence.csv` and the tables still
written for diagnosis.

### What is rejected

The gates answer with `422` and name the fix:

```bash
curl -s -X POST 'localhost:8080/validate?target=calliope' -d @examples/pypsa_power_flow.json
# {"error":"mode \"power_flow\" is not supported by target \"calliope\"","valid":false}

jq 'del(.model.technologies.dg)' examples/pypsa_power_flow_reactive.json \
  | curl -s -X POST 'localhost:8080/validate?target=pypsa' -d @-
# {"error":"AC island [mv3] has no supply technology to balance it; PyPSA cannot solve a sub-network
#   without a generator","valid":false}

jq 'del(.experiment.power_flow.slack)' examples/pypsa_power_flow.json \
  | curl -s -X POST 'localhost:8080/validate?target=pypsa' -d @-
# {"error":"experiment.power_flow.slack is required: the supply technology that balances the AC network (id, or id@node)"}

jq '.experiment.power_flow.distribute_slack = true' examples/pypsa_power_flow.json \
  | curl -s -X POST 'localhost:8080/validate?target=pypsa' -d @-
# {"error":"experiment.power_flow.distribute_slack shares the balance in proportion to each generator's dispatch,
#   so the slack \"grid\" needs a dispatch too (its expected output); ..."}
```

Power-flow inputs in a planning payload are not an error. Sending the
reactive payload in mode `plan` to Calliope answers `valid: true` with one
warning that names everything the optimization ignores, so the same document
serves both calls:

```bash
jq '.experiment = {"mode":"plan","solver":{"name":"cbc"}}' examples/pypsa_power_flow_reactive.json \
  | curl -s -X POST 'localhost:8080/validate?target=calliope' -d @-
# {"valid":true,"warnings":["power-flow inputs are only used by mode power_flow on a target with the
#   power_flow capability; in mode \"plan\" on target \"calliope\" they are ignored: dispatch/electrical on
#   technologies [dg grid load1 load2 load3 pv], electrical/dispatch on transmission [hvdc l12 trafo]
#   (emitted as transport links), electrical on nodes [hv mv1 mv2 mv3]. Pin a dispatch in an
#   optimization with operation.equals_pu instead"]}
```

## Planning on the electrical network

`examples/pypsa_planning.json` is the PyPSA planning payload that uses what
the power-flow payloads describe: the same `electrical` fields, now inside
the optimization (see [ADR-0010](../decisions/0010-electrical-network-inside-optimization.md)
and [ADR-0011](../decisions/0011-pypsa-optimization-completeness.md)).

```bash
ID=$(curl -s -X POST 'localhost:8080/simulate?target=pypsa' -d @examples/pypsa_planning.json | jq -r .id)
until curl -s "localhost:8080/jobs/$ID/status" | jq -e '.state | test("succeeded|failed")' >/dev/null; do sleep 1; done
curl -s -o planning.zip "localhost:8080/jobs/$ID"
unzip -p planning.zip files/run_0/output/results/capacities.csv   # nominal and optimal per component: Line ab, Generator wind (10 MW modules), old (retired part), Store battery, Links battery_charge/discharge
unzip -p planning.zip files/run_0/output/results/lines-p0.csv     # flows under Kirchhoff's law
unzip -p planning.zip files/run_0/output/results/summary.json     # objective, status
```

- `experiment.network.model: linear_power_flow` turns every arc with
  `electrical` into a Line or a Transformer the optimizer solves under the
  voltage law; `capacity.expandable` on such an arc expands the line (the
  impedance stays fixed), `max_loading` caps its loading, and `lifetime`,
  `interest_rate` and `costs.monetary.investment_per_capacity` price it.
  `network.security.outages: ["ab"]` would make the dispatch survive that
  outage (mode `plan`, no portable constraints).
- `capacity.per_unit` on an expandable component builds integer modules;
  `decommission.mode: continuous` lets existing capacity retire down to what
  the horizon uses, at its fixed O&M; `allow_unmet_demand` adds a slack
  generator per load bus priced at `penalty_price`.
- A storage with `storage.energy_capacity` is a Store with charger and
  discharger; `max_charge_rate` and `max_discharge_rate` bound their power
  against the energy built, `depth_of_discharge` is the minimum state of
  charge, `investment_per_energy_capacity` prices the energy.
- The committable unit's `ramp_start_up`, `ramp_shut_down`, `standby_cost`
  and `initial_uptime` are PyPSA's unit-commitment details; a
  `variable_om_quadratic` would be a quadratic cost (not together with
  integer variables on the free solvers).
- `model.periods` with years turn the plan into investment periods (see
  the `pypsa_multi_period` scenario): snapshots become (period, timestep),
  an expandable technology is built per period, `build_year` and `lifetime`
  retire what ages out.

This is a PyPSA payload. Sent to Calliope or AdOpT-NET0 it is rejected on
the network model alone, like an unsupported mode, before anything else is
looked at; without the `network` block the committable unit's start-up cost
and minimum up-time would still have no mapping there. The shared examples
above show what every target solves; fields a target has no slot for
(`soc_profile`, the unit-commitment extras, a quadratic cost, investment
periods) are kept and named in a warning rather than rejected.
