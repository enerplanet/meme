# API Examples

Four payloads — one **maximal** config per framework (every property that
target honors per [CAPABILITY.md](../docs/CAPABILITY.md)) plus one **shared** config
valid for all three at once — and the API calls to drive them. All four have
been executed against the real API with real solvers and run to `succeeded`
(PyPSA/HiGHS, Calliope/CBC, AdOpT/GLPK).

| File | Target | Exercises |
|---|---|---|
| [`pypsa_full.json`](pypsa_full.json) | `pypsa` | 3 carriers, 2 nodes (coords + native bus columns), physics PV with precomputed CF + per-node overrides, committable CCGT (full UC set + ramps + min/max_pu), time-varying marginal cost, multi-output CHP (rigid ratios), battery (cyclic, self-discharge, initial SOC), hydro (inflow series + spill cost, non-cyclic), bidirectional line with native `x`/`r`, trade with **series** import price + emission factor + export revenue, CO₂ cap (GlobalConstraint), one unscoped + two scoped/multi-term constraints (run.py sidecar), a **series import limit** (fixed-p_nom generator + time-varying `p_max_pu`), per-distance line losses folded into the link efficiency, snapshot **weights**, `build_year`/`annual_output_max`/`systemwide_max`, typed solver `time_limit`/`threads`, tech/carrier/system native blocks, and a 2-point **sweep** → 2 independent runs |
| [`calliope_full.json`](calliope_full.json) | `calliope` | availability data table from a CF series, first-class `source` + `area` (per-area resource on a bounded area), **MILP committable** (integer 50-MW units + purchase cost + min stable load), multi-output CHP via **ratio-pinning math**, **piecewise** part-load conversion (fixed 300 MW), battery with energy-capacity costs (`cost_storage_cap`) + `cost_om_annual` + **depth of discharge**, transmission (bidirectional, distance, **per-distance investment cost**), `reporting.save_logs`, typed solver `time_limit`, trade (import price + co2 factor, paid export), CO₂ cap + scoped constraint (custom math), native `flow_out_parasitic_eff`, and **resample 1H → 3H** |
| [`adopt_full.json`](adopt_full.json) | `adopt-net0` | full climate block (ghi/dni/dhi/temp_air/rh/ws10; series and scalars mixed), three physics techs (`pv` → Photovoltaic, `wind` → WindTurbine, `heat_pump` → HeatPump_AirSourced serving a heat demand), battery on two nodes with a **per-node override** (`size_max` 100 vs 40), tech-level native patch, trade at both nodes with a **series** import price + import/**export** emission factors, `reporting.case_name`, typed solver `time_limit`/`mip_gap`, and a CO₂ emission cap (`costs_emissionlimit` objective) |
| [`shared_full.json`](shared_full.json) | **`all`** | The maximal **intersection**: one config valid for all three frameworks *simultaneously* — physics PV + wind carrying both a climate block (AdOpT computes output itself) and precomputed CF profiles (PyPSA/Calliope), a two-node battery with a per-node override, scalar-priced trade at both nodes (+ import co2 factor), an emission cap, a battery-capacity **sweep**, a global **`discount_rate`** and typed solver `time_limit` (both honored by all three), and a per-target `native` block on the same tech (each framework picks only its own). Submitted once with `?target=all` → **6 solves** (3 frameworks × 2 sweep points) in one job, one zip |
| [`payload_full.json`](payload_full.json) | **`all`** | `shared_full` pushed to the limit, with **all native scopes from the three per-target payloads riding together**: three physics techs (PV, wind, heat pump — each with climate data for AdOpT *and* a precomputed profile for PyPSA/Calliope), two carriers, capacity `min`, and native blocks at every honored scope — carrier (`nice_name`), node (`v_nom`), model (`_native.pypsa.json`), and a battery carrying `pypsa` + `calliope` + `adopt-net0` fragments side by side. Each framework consumes only its own key; the validate response's warnings read like a routing table of who ignored what. Verified: `?target=all` → 6/6 optimal solves |

The three per-target payloads are deliberately *not* portable — and a raw
*union* of them cannot validate anywhere: `pypsa_full`'s UC extras are rejected
by Calliope, `calliope_full`'s piecewise tech by PyPSA, the CHP/constraints/
transmission by AdOpT (see §7). `payload_full.json` is the honest maximum of
"one config, three frameworks": the largest shared property set, with each
framework's extras isolated in its own `native` namespace.

## 0. Start the API (real solvers)

```bash
make docker-run ENV=dev          # API on :8080 with PyPSA + Calliope + AdOpT + solvers
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
# -> {"targets":["pypsa","calliope","adopt-net0"], "features":{"pypsa":{"mode:stochastic":true,…},…}}
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
[`internal/scenarios/testdata/scenarios/`](../internal/scenarios/testdata/scenarios/):

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
