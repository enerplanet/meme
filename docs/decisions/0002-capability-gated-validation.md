# ADR-0002: Validation is capability-gated; nothing a payload asks for is silently dropped

- **Status:** accepted
- **Date:** 2026-07-13 (recorded 2026-10-24 from the initial design)

## Context

The same canonical payload is sent to frameworks of very different
expressiveness. A translator that quietly leaves out what a framework cannot
represent produces models that *look* equivalent and are not: a cost
minimised without a constraint the user wrote, a node without the demand the
user placed there. Such differences are invisible in a successful solve and
surface only as a wrong number.

## Decision

Every target declares a **capability profile** (`Capabilities()`), a sparse
map from feature keys to `true`. The registry's generic gates reject a
payload that uses a feature the selected target does not claim, before any
target-specific code runs. Each target adds its own gates (`ValidateJob`)
for everything its emitter cannot render faithfully, and must do one of two
things with such a property: **reject** it with an error that names the
property and the target, or **warn** that it is approximated or dropped. A
property is never accepted and then ignored.

The claims are enforced in both directions: a capability-coverage test fails
the build if a claimed feature has no runnable scenario behind it, and the
frozen validation matrix fails the build if a verdict changes unnoticed.

## Consequences

- `POST /validate` is a real contract: `200` means the model the solver sees
  is the model the user wrote (up to the listed warnings); `422` names the
  first offending property.
- Adding a feature means a schema field, a feature key, a scenario and the
  gates — more ceremony than an ad-hoc `if`, but every cell of the
  capability matrix stays provable.
- Partial support is expressed as a gate on the shape, not as a silent
  fallback: Calliope 0.6 accepts single-term constraints and rejects
  multi-term ones with a pointer to the 0.7 target.
- The cost is friction for users who want "best effort": there is no mode
  that ignores unsupported properties, by design. A warning-only policy for
  a documented set of informational fields (`build_year`, `metadata`) is
  the one concession.
