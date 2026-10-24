# ADR-0004: Golden files and a validation matrix freeze the emitter contract

- **Status:** accepted
- **Date:** 2026-07-14 (recorded 2026-10-24 from the initial design)

## Context

An emitter change can alter every model the service produces, and the only
authoritative check — solving on the real framework — takes a container,
minutes, and a solver. Reviewing a translation change by reading Go is
error-prone; reviewing it by reading the emitted model is not.

## Decision

Every embedded scenario is emitted for every target that accepts it, and the
complete emitted tree (native model files, the run plan, any generated driver
script) is compared **byte for byte** against committed golden files under
`internal/target/testdata/golden/<target>/<scenario>/`. The exact
accept/reject verdict and every warning text for every scenario × target
pair are frozen in one `validation_matrix.json`. Emission is deterministic
by construction (sorted map iteration everywhere), so the goldens never
flake. An intended change is accepted with `make golden-update`, and the
golden diff is reviewed as part of the change.

## Consequences

- Most translation regressions are caught in milliseconds on the host,
  long before a solver sees them; the real-solver E2E suite certifies the
  result rather than discovering it.
- A pull request's golden diff *is* the contract change, readable by anyone
  who knows the target framework, Go or not.
- The goldens are large and churn with every emitter change (a file rename
  touches dozens of files). The review discipline is to check that content
  changes are the intended ones and renames are byte-identical.
- Goldens cannot prove a model is *solvable*; the E2E tiers pin objective
  values on real solvers for that, including the same payload across
  frameworks and across Calliope releases.
