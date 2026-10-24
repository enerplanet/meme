# ADR-0003: Framework-only concepts travel in per-target `native` blocks

- **Status:** accepted
- **Date:** 2026-07-13 (recorded 2026-10-24 from the initial design)

## Context

Each framework has concepts no other one shares: PyPSA's power-flow
electrics, Calliope's user-defined math, AdOpT-NET0's technology database
and climate preprocessing. Abstracting them into the canonical schema would
either invent a lowest common denominator nobody uses or grow the schema
into a union of three frameworks that no target could honor.

## Decision

A concept that **more than one framework** supports becomes a first-class
canonical field, emitted for every applicable target. A concept **only one
framework** has is supplied through that framework's `native` block (on a
technology, node, carrier, transmission arc, trade entry, the model or the
experiment), keyed by target name — `native.pypsa`, `native.calliope`,
`native["calliope-0.6"]`, `native.adopt-net0` — and merged **last and
verbatim** into that framework's emitted output. Blocks for other targets are
ignored and reported as such; using a block marks the payload target-locked
in a warning.

## Consequences

- By construction nothing expressible in a framework is lost: the escape
  hatch is the framework's own vocabulary, unvalidated by MEME.
- The canonical schema stays small and honest; every field in it is emitted
  by at least two targets.
- Framework versions with different dialects need **separate keys**
  (`native.calliope` is 0.7 syntax, `native["calliope-0.6"]` is 0.6 syntax),
  which [ADR-0006](0006-framework-versions-as-targets.md) builds on.
- Native values are not validated and can override emitted attributes, so a
  payload using them trades portability and safety for power; the warning
  makes that trade visible.
