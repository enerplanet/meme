# `examples/` — ready-to-run payloads

One **maximal** payload per framework (every property that target honors),
two **shared** payloads valid for all frameworks at once, and a PyPSA
**power-flow** payload (the second call after a plan); all have been run
against the real API with real solvers.

| File | Target |
|---|---|
| [`pypsa_full.json`](pypsa_full.json) | `pypsa` |
| [`pypsa_power_flow.json`](pypsa_power_flow.json) | `pypsa`, mode `power_flow` (AC power flow of a fixed dispatch; no optimization) |
| [`pypsa_power_flow_minimal.json`](pypsa_power_flow_minimal.json) | `pypsa`, mode `power_flow`: the smallest valid power flow (one line, slack and load) |
| [`pypsa_power_flow_meshed.json`](pypsa_power_flow_meshed.json) | `pypsa`, mode `power_flow`: meshed 20 kV ring, per-node dispatch and demand, parallel transformers, slack as `tech@node` |
| [`pypsa_power_flow_options.json`](pypsa_power_flow_options.json) | `pypsa`, mode `power_flow`: linear method, distributed slack, storage sign convention, an unbalanced second carrier |
| [`pypsa_power_flow_reactive.json`](pypsa_power_flow_reactive.json) | `pypsa`, mode `power_flow`: reactive power, voltage control, a shunt, a controlled HVDC link, per-length impedance, phase shift |
| [`pypsa_planning.json`](pypsa_planning.json) | `pypsa`: planning under the voltage law with line expansion, modular capacity, continuous retirement, unit-commitment extras, a Store-based battery and priced slack demand |
| [`calliope_full.json`](calliope_full.json) | `calliope` |
| [`adopt_full.json`](adopt_full.json) | `adopt-net0` |
| [`shared_full.json`](shared_full.json) | `all` (also valid for `calliope-0.6`) |
| [`payload_full.json`](payload_full.json) | `all`, with every framework's `native` block side by side |

```bash
make -C environment run ENV=dev                                            # API with real solvers
curl -s -X POST 'localhost:8080/validate?target=all' -d @examples/shared_full.json | jq .
ID=$(curl -s -X POST 'localhost:8080/simulate?target=all' -d @examples/shared_full.json | jq -r .id)
curl -s -o result.zip "localhost:8080/jobs/$ID"
```

Every payload carries `"api_key": "s3cret"`, which only matters when the
server was started with a matching `API_KEY`. The full walkthrough — what each
payload exercises, validate / convert / simulate, every run mode, multi-target
jobs and what a rejection looks like — is in the documentation:
**<https://enerplanet.github.io/meme/getting-started/examples/>**.
