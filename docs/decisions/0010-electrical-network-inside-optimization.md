# ADR-0010: The electrical network enters the optimization: linear power flow, line expansion, security constraints and transmission economics

- **Status:** accepted
- **Date:** 2026-10-25

## Context

ADR-0008 and ADR-0009 describe the AC network in the canonical model and
solve it as a power flow of a fixed dispatch. In every optimization mode the
same arcs still become controllable Links: the optimizer sees capacities and
efficiencies, never Kirchhoff's voltage law, and expands a Link when an arc
is expandable. PyPSA's distinctive capability — dispatch and expansion under
the linear power-flow equations, with the option of surviving the outage of
any branch — therefore stays unreachable, although every input it needs is
already in the payload. Transmission economics were a second gap: an arc
carried no lifetime or interest rate, so its investment cost could not be
annualized and was warned about and dropped on PyPSA.

PyPSA 1.2.4 builds the voltage-law constraints automatically when a
sub-network contains Lines with a reactance, expands `s_nom_extendable`
Lines (with the impedance held fixed, the documented approximation), honors
`s_max_pu` as a loading limit, and solves the security-constrained problem
with `optimize_security_constrained(branch_outages)`.

## Decision

The run specification gains `experiment.network`:

- `model`: `transport` (the default and today's behaviour: every arc is a
  Link) or `linear_power_flow`. With the latter PyPSA emits the electrical
  carrier's buses with their voltage level, every arc that carries
  `electrical` as a Line or a Transformer — `s_nom` from the rating,
  `s_nom_extendable`, `s_nom_min` and `s_nom_max` from the arc's `capacity`,
  `s_max_pu` from the new `electrical.max_loading`, `capital_cost` from the
  arc's costs — and solves the optimization under the voltage law. Arcs
  without `electrical` stay Links, so a controlled tie in an otherwise
  electrical network is expressed by leaving the impedance out.
- `security.outages`: a list of electrical arcs whose single outage the
  dispatch must survive; PyPSA runs the security-constrained solve. It needs
  `model: linear_power_flow` and mode `plan`, and it cannot carry the
  portable constraints the sidecar applies, like the operate and stochastic
  runners.

The network model is a capability, `linear_power_flow`, which PyPSA alone
claims; every other target rejects `model: linear_power_flow` the way it
rejects an unsupported mode, because a transport solve of a payload that
asked for the voltage law is a different problem, not an ignorable input.
The `transport` default keeps every existing payload and every other target
unchanged.

Transmission gains `lifetime`, `interest_rate` and `cost_basis`, and PyPSA
emits `capital_cost` on Links and Lines from `investment_per_capacity` plus
`investment_per_capacity_distance` times `distance`, annualized like a
technology's investment; fixed O&M folds in the same way. Calliope keeps its
per-distance cost mapping and the other transmission cost fields keep their
warning there; nothing it accepted changes meaning.

Every optimization run now writes a `results/` folder next to `network.nc`:
per-component dispatch and flows, storage states, bus marginal prices, the
optimal capacities and PyPSA's statistics table, so the API consumer that
reads the power-flow tables reads the planning tables the same way.

## Consequences

- Network-constrained dispatch and line expansion become one switch on a
  payload that already carries the electrical fields; the same payload runs
  as a transport model on Calliope. The consumer decides per call.
- Line expansion holds the impedance fixed. The iterative reactance update
  (`optimize_transmission_expansion_iteratively`) is a possible later option;
  it is not claimed.
- Security constraints multiply the problem by the number of outages; the
  list is explicit so the cost is visible, and `all` is deliberately absent.
- Out of scope: DC sub-networks, reactive power inside the optimization
  (linear power flow is active-power only), and line-type catalogues.
