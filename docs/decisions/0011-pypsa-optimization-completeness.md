# ADR-0011: PyPSA optimization completeness: Store-based storage, modular capacity, retirement, slack demand, copperplate, unit-commitment extras and multi-period investment

- **Status:** accepted
- **Date:** 2026-10-25

## Context

A review of PyPSA's input model against the canonical schema left a list of
things a PyPSA user configures that MEME could not express or expressed
only through `native.pypsa`: energy-only storage with independently sized
charger and discharger, integer-module capacity expansion, retirable
existing capacity, a penalized slack for unmet demand, a copperplate run,
the unit-commitment details beyond start and stop costs, quadratic variable
costs, a fixed state-of-charge trajectory, and investment over several
periods. Each has a canonical home already — most are existing fields that
other targets honor and PyPSA rejects or drops — and the rule of ADR-0002
says a field a target can honor should not be dropped there.

## Decision

Each item maps onto PyPSA through the field that already means it, with the
representation chosen by what the payload says, never by a PyPSA-specific
switch:

- **Storage as a Store.** A storage technology with `storage.energy_capacity`
  becomes a `Store` on its own bus with a charger and a discharger `Link`:
  the energy capacity, its bounds, its expandability and
  `investment_per_energy_capacity` sit on the Store (`e_min_pu` from
  `depth_of_discharge`, `e_initial` from `initial_soc` of a fixed capacity,
  `e_cyclic`, `standing_loss`), the power capacity and its costs on the
  Links. Two structural constraints travel in a `_storage.json` sidecar the
  driver applies: equal charger and discharger power when the power
  capacity is a decision, and `max_charge_rate` / `max_discharge_rate` as
  bounds of link power against store energy. A storage without
  `energy_capacity` stays a `StorageUnit`; `soc_profile` (a fixed
  state-of-charge series) maps onto its `state_of_charge_set`.
- **Modular capacity.** `capacity.per_unit` on an expandable component
  becomes PyPSA's integer module (`p_nom_mod`, `e_nom_mod`, `s_nom_mod`); the
  unit bounds stay as `p_nom_min` / `p_nom_max`.
- **Retirement.** `capacity.decommission.mode: continuous` splits a
  component into its existing part, retirable down to zero at its fixed
  O&M, and, when expandable, a new-build part that carries the investment
  cost. `only_complete` stays rejected (a binary PyPSA has no slot for).
- **Slack demand.** `allow_unmet_demand` adds a slack generator at every
  load bus of the carrier, priced at `penalty_price`; **copperplate** merges
  every node's bus of a carrier into one and drops the arcs, as AdOpT does.
- **Unit-commitment extras and costs.** `operation.ramp_start_up`,
  `ramp_shut_down`, `standby_cost`, `initial_uptime` and `initial_downtime`,
  `storage.soc_profile`, and `costs.<class>.variable_om_quadratic` map onto
  the PyPSA attributes of the same meaning. A quadratic cost together with
  an integer variable (committable, modular capacity) is a MIQP the free
  solvers cannot solve, so it is rejected unless the solver is gurobi.
- **Multi-period investment.** `model.periods` with years become PyPSA's
  `investment_periods`; snapshots become (period, timestep); every series
  is repeated per period; `build_year` and `lifetime` are emitted, and the
  solve runs with `multi_investment_periods`. The capability is
  `multi_period`, claimed by PyPSA; a target that does not claim it treats
  `periods` as today (accepted, single horizon) with a warning.

Fields other targets cannot honor are handled as before: Calliope and
AdOpT-NET0 keep every mapping they have, and the new fields they have no
concept for are ignored and named in a warning, so a payload that plans on
one target and on PyPSA stays one document.

## Consequences

- Validation verdicts move on PyPSA from "rejected" to "honored" for storage
  rate bounds, depth of discharge, energy-capacity costs, decommissioning,
  unmet demand and copperplate; the validation matrix records every change.
- The Store topology adds two Links and a bus per storage; result tables
  report the store's energy and the links' power, and the statistics table
  attributes them to the technology's carrier.
- Retirement without a separate salvage value is driven by fixed O&M only;
  a retirement cost has no PyPSA slot and stays rejected.
- Multi-period runs multiply the snapshot index by the number of periods;
  time series are per horizon, not per period, until a per-period series
  form is needed.
