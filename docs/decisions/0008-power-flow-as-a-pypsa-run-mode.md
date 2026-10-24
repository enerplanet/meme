# ADR-0008: Power flow is a PyPSA run mode over an electrical network in the canonical model; MEME never chains jobs

- **Status:** accepted (amended 2026-10-24)
- **Date:** 2026-10-24

## Context

Calliope and AdOpT-NET0 are transport models: energy balances per node, arcs
with a capacity and an efficiency, no Kirchhoff voltage law. PyPSA has what
they lack, an electrical network with `pf()` and `lpf()`. The run that adds
information after a Calliope plan is therefore not a second optimization but
a power flow of the planned dispatch on the physical grid: bus voltages, line
and transformer loading, convergence per snapshot.

MEME could not express that run. The canonical `Transmission` carries
capacity, efficiency, distance and loss but no impedance, rating or voltage
level, so every arc became a controllable PyPSA Link and `power_flow` was
deliberately not claimed. Every `experiment.mode` was an optimization and the
generated driver only ever called the optimize family. The bundle held the
solved network and the log, not per-bus or per-branch results.

A branch (`feature/power-flow`, 2026-10-07) closed the gap with a top-level
`power_flow` block beside `model` and `experiment`: a complete second network
(buses, lines, transformers, fixed injections) that the emitter wrote
*instead of* the model, with run.py forced into a power-flow mode by the
block's presence. That design hands one framework's result to another
through a private side channel: the block is not part of the canonical
model, bypasses its validation, duplicates the node and technology
vocabulary, and makes the consumer's chaining of Calliope into PyPSA a
hidden MEME feature.

## Decision

A power flow is an ordinary run of the PyPSA target, configured entirely
through the canonical model and experiment, and the API consumer is
responsible for the chaining: one call plans on Calliope, a second call
hands PyPSA the resulting dispatch. MEME holds no state between the two and
reads no result of one job as the input of another.

The canonical model gains three electrical fields, all optional and all
power-flow inputs:

- `node.electrical.v_nom` — the nominal voltage (kV) of the node's AC bus.
- `transmission.electrical` — makes an arc an AC branch: `kind` `line`
  (series impedance `r`, `x` in Ω over the arc) or `transformer` (`r`, `x`
  per unit of its rating, optional `tap_ratio`), an `s_nom` rating in MVA
  that defaults to the arc's installed capacity, and `num_parallel`. An
  electrical arc is bidirectional, a line connects two nodes of equal
  `v_nom`.
- `technology.dispatch` — the fixed active-power output per timestep (MW)
  the power flow injects for a supply, conversion or storage technology
  (PyPSA `p_set`); demand keeps `demand_profile`. It is overridable per node.

The experiment gains the mode `power_flow` with its own typed block,
`experiment.power_flow`: `method` (`newton_raphson`, the default, seeded by a
linear solve; or `linear`), `slack` (the supply technology that balances the
network; required), `distribute_slack` and `tolerance`. The mode maps onto
the existing `power_flow` capability, which only PyPSA claims; every other
target rejects the mode through the generic mode gate. A payload that
carries `dispatch` in an optimization mode is rejected on every target,
with a pointer to `operation.equals_pu`, because there the solver decides
dispatch. Electrical fields in an optimization mode, or on a target without
the capability, are reported as unused in a warning and the arcs are emitted
as transport links, so one model serves the planning call and the power-flow
call. In mode `power_flow` nothing may be a decision: expandable
capacities, trade, constraints, emission limits and scenarios are rejected.

The PyPSA emitter writes the AC network from these fields: the electrical
carrier's buses get carrier `AC` and `v_nom`, lines and transformers get
their own tables, generators their `p_set` and `control` (`Slack` for the
named technology, `PQ` otherwise), storage units and conversion links their
`p_set`; series dispatch is materialized like every other series. The
driver's `power_flow` branch runs `lpf()` then `pf()` (or `lpf()` alone),
exports the network, and writes a `power_flow/` folder in the run output:
`convergence.csv`, per-bus voltage magnitude, angle, active and reactive
power, per-line and per-transformer flows and loading, generator dispatch,
and a `summary.json`. A snapshot that does not converge fails the run (exit
code 3) with the tables still written for diagnosis; the log carries
`power flow converged k/n snapshots`.

## Consequences

- The consumer's workflow is two explicit API calls with two payloads it
  owns: a planning payload for Calliope (or PyPSA) and a power-flow payload
  for PyPSA whose `dispatch` series it fills from the planning result. MEME
  stays a stateless translator and the canonical model stays the single
  entry point; nothing arrives through a side channel.
- The electrical fields are honest about their reach: the capability matrix
  shows `power_flow` for PyPSA only, the warning names every arc and node
  whose electrical parameters an optimization run ignores, and the
  validation matrix freezes the verdicts.
- The power flow is active-power only in this version: loads at unity power
  factor, generators `PQ` with zero reactive set-point, the slack bus
  holding voltage. Reactive power set-points, `PV` voltage control, shunt
  elements and a line-type catalogue are deliberately out of scope; each
  would be an additive field with its own gate.
- Network-constrained optimization (PyPSA Lines with impedance inside
  `optimize`, security-constrained dispatch) is not part of this decision.
  Optimization modes keep emitting transport links even when electrical
  parameters are present; claiming KVL inside an optimization would be a new
  capability with its own record.
- The rejected branch stays rejected: a top-level network block would
  reappear as soon as a consumer wanted a shape the canonical model cannot
  express. The answer to that is a canonical field with a gate, as here.

## Amendments

- **2026-10-24.** The rejection of `dispatch` in optimization modes is
  superseded by [ADR-0009](0009-reactive-power-voltage-control-and-fixed-transfers.md):
  every power-flow input is ignored with a warning wherever no power flow
  runs, on Calliope and AdOpT-NET0 and on PyPSA's optimization modes alike,
  so one payload serves the planning call and the power-flow call. The mode
  gate is unchanged. ADR-0009 also adds reactive power, voltage control,
  shunts and fixed transfers to the network described here.
