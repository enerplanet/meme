# `test/` — the test pyramid

Everything about testing MEME lives behind [`Makefile`](Makefile); the root
Makefile aliases every target, so `make test` and `make -C test test` are the
same thing.

```bash
make test           # vet + all host tests (pure Go, no Python; seconds)
make test-race      # race detector over internal/...
make golden-update  # accept an INTENDED emitter change into the golden files
make schema-check   # examples/ + scenario corpus vs the JSON Schema (python3 + jsonschema)
make e2e-smoke      # container: one real-solver lifecycle per target, the PyPSA power-flow checks, multi-target (~1 min)
make e2e            # container: the whole corpus on real solvers, both Calliope releases (~2 min)
```

The layers (unit tests, golden files and the frozen validation matrix, the
real-solver E2E tiers), the golden workflow, how to add a scenario or an
objective pin, and the fuzzers are described in the documentation:
**<https://enerplanet.github.io/meme/operations/testing/>**.
