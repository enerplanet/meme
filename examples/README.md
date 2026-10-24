# `examples/` — ready-to-run payloads

One **maximal** payload per framework (every property that target honors) and
two **shared** payloads valid for all frameworks at once; all have been solved
against the real API with real solvers.

| File | Target |
|---|---|
| [`pypsa_full.json`](pypsa_full.json) | `pypsa` |
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
