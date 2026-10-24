# ADR-0009: Reactive power, voltage control, shunts and fixed transfers complete the power-flow network; power-flow inputs are ignored where no power flow runs

- **Status:** accepted
- **Date:** 2026-10-24

## Context

ADR-0008 made the AC network canonical for an active-power flow: voltage
levels, line and transformer impedances, one fixed active dispatch per
injection, a slack. That is enough to see angles, active flows and the
voltage sag they cause, but not what a distribution planner checks next:
reactive power, which decides voltage magnitudes, voltage-controlled
generation, compensation at a node, and controlled DC transfers between
AC buses. PyPSA solves all of these in `pf()` (`q_set` on loads, generators
and storage units, `control: PV` with the bus `v_mag_pu_set`,
`ShuntImpedance`, `p_set` on a Link between AC buses, line `b`/`g`,
transformer `phase_shift`). Reaching them through `native.pypsa` works for
constants only, is unvalidated, and cannot add a component MEME does not
emit.

The same decision has to settle what the other targets do with these
inputs. ADR-0008 rejected `dispatch` in every optimization mode and merely
warned about electrical parameters. The consumer's workflow is one model
sent twice, to a planning target and to the power flow, and a rejection on
the planning side forces a second, stripped copy of the payload for no
gain: an optimization decides dispatch and never reads reactive set-points,
so ignoring them changes nothing in its result.

## Decision

The power-flow network gains four groups of fields, each on the entity it
physically belongs to, each read only in mode `power_flow`, each mapped onto
one PyPSA attribute:

- **Technology `electrical`** (overridable per node): `reactive_dispatch`
  (MVAr per timestep, `q_set`) or `power_factor` with optional `leading`
  (the emitter derives Q from the active set-point in the component's own
  direction: a load consumes, a generator supplies), `control` `pq` or `pv`
  (supply only; `pv` holds its bus at `voltage_setpoint`), and
  `voltage_setpoint` (pu) for `pv` generators and the slack. Conversion
  technologies carry no reactive fields: a PyPSA Link transfers active
  power only.
- **Node `electrical.shunt`** (`g`, `b` in siemens): one `ShuntImpedance`
  at the node's AC bus.
- **Transmission `dispatch`** (MW from `from` to `to`): a controlled
  transfer on an arc without impedance, PyPSA's model of an HVDC link.
  An electricity arc in a power flow needs either `electrical` or
  `dispatch`, never both: a line's flow is a result.
- **Branch refinements in `transmission.electrical`**: `b` and `g` (line
  shunt admittance), `phase_shift` (transformer, degrees), and
  `r_per_length`/`x_per_length` as the alternative to `r`/`x`, multiplied
  by `distance` at emission. Cable-type catalogues stay outside MEME: the
  consumer resolves the type, MEME takes values.

Every power-flow input — `dispatch` on technologies and arcs, the
`electrical` blocks on nodes, arcs and technologies — is **ignored with one
warning** wherever no power flow runs: on Calliope and AdOpT-NET0, which
have no such concept, and on PyPSA in every optimization mode. The warning
names the fields and the entities, so nothing is dropped silently, and the
planning payload and the power-flow payload may be the same document. This
supersedes the rejection of `dispatch` in optimization modes that ADR-0008
introduced. Mode `power_flow` itself stays rejected on targets without the
capability: a run cannot be ignored.

Inside a power flow the gates tighten instead: a reactive set-point and a
power factor are mutually exclusive; `pv` control is for supply
technologies; a `voltage_setpoint` needs `pv` control or the slack; all
set-points at one bus must agree; `method: linear` reports that reactive
quantities and voltage control play no part in a DC solve.

## Consequences

- A planner can hold voltages, see reactive flows and compensation effects,
  and model HVDC ties, all through canonical fields with validation and
  goldens; the result folder gains the reactive tables and the shunt tables.
- The consumer sends one model to both calls and reads the warning list on
  the planning side as a routing table of what that target ignored.
- The derived-Q path (`power_factor`) is computed by the emitter per
  snapshot, so a series active set-point yields a series `q_set`; the two
  ways to give Q cannot drift apart because only one may be present.
- Still out of scope: DC sub-networks (buses of carrier `DC` with lines),
  tap changers and SVCs that act during the solve, time-varying voltage
  set-points, and anything `pf()` does not model. `tap_side`, the
  transformer `model` and line derating stay native-only.
- ADR-0008 is amended, not rewritten: its dispatch-rejection consequence is
  superseded by this record.
