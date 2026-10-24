# ADR-0001: MEME is one dependency-free Go binary; the solvers are external processes

- **Status:** accepted
- **Date:** 2026-07-10 (recorded 2026-10-24 from the initial design)

## Context

The service translates one canonical model into three frameworks' native
inputs and drives their solvers. The frameworks are Python packages with
mutually conflicting dependency sets (Calliope's pins collide with PyPSA's
and AdOpT's; Calliope 0.6.8 cannot even share an interpreter with Calliope
0.7.0). Embedding them, or writing the translator in Python next to them,
would tie the API's build and runtime to that dependency tangle and make the
translation logic as hard to test as the solvers are slow.

## Decision

MEME is a single Go program using only the standard library: no third-party
Go modules, no cgo. It owns validation, translation (the emitters), job
orchestration and the HTTP API. Every solver is an **external process**: a
generated `run.py` for PyPSA and AdOpT-NET0, the `calliope` / `calliope-0.6`
CLIs for Calliope. The `environment/` image provides those interpreters, one
virtual environment per conflicting dependency set, and exposes the CLIs on
`PATH`; the service itself needs none of them to validate and translate.

## Consequences

- The translator is testable in milliseconds without Python: golden files
  snapshot every emitted model and a frozen validation matrix freezes every
  verdict ([ADR-0004](0004-golden-files-freeze-the-emitter-contract.md)).
  The default runner is a dry-runner, so the API works on a bare host.
- Dependency conflicts between frameworks are an *environment* concern
  (separate venvs), not a build concern. Adding a framework version means
  another venv and another CLI name, not a Python packaging negotiation.
- Everything the frameworks compute (annualization in Calliope, power flow
  in PyPSA) happens on their side; MEME must translate faithfully but never
  re-implements a solver.
- A YAML writer, CSV writers and a JSON schema validator had to be written
  by hand (the YAML writer is fuzz-tested). That is the price of zero
  dependencies and was judged smaller than the alternative.
- Revisit if a translation genuinely needs a framework's own library at
  emit time (for example to resolve a framework's defaults); so far the
  pinned schemas and the real-solver E2E suite have been enough.
