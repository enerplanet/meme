# Quickstart

**Dry-run on the host** (Go only, no Python — validates, translates and plans,
but does not solve):

```bash
make run                                    # build + serve on :8080
curl -s -X POST 'localhost:8080/validate?target=all' \
     -d @examples/shared_full.json | jq .   # per-target verdicts
curl -s -X POST 'localhost:8080/convert?target=calliope' \
     -d @examples/calliope_full.json | jq . # emit the native model to disk
```

Without a checkout, `go install github.com/enerplanet/meme/cmd/meme@latest`
puts the same dry-run binary into your Go bin directory (`meme -version`
tells you which release).

**Real solvers in Docker** (PyPSA/HiGHS, Calliope/CBC, AdOpT/GLPK — nothing to
install on the host beyond Docker):

```bash
make -C environment build ENV=dev           # one-time image build
make -C environment run   ENV=dev           # API on :8080, real execution

ID=$(curl -s -X POST 'localhost:8080/simulate?target=all' \
     -d @examples/shared_full.json | jq -r .id)          # 202, async job
curl -s "localhost:8080/jobs/$ID/status" | jq .state     # queued->running->succeeded
curl -s -o result.zip "localhost:8080/jobs/$ID"          # results + config + files + logs
```

The full request walkthrough (single- and multi-target, every run mode, seeing
a 422 rejection) is on the [Examples](examples.md) page.


## Next steps

- [Configuration](configuration.md): ports, work directory, real execution,
  API key and CORS.
- [Examples](examples.md): the full request walkthrough, single- and
  multi-target, every run mode, and what a rejection looks like.
- [Targets and versions](../targets.md): which framework release each
  target drives and how `all`, variants and aliases resolve.
