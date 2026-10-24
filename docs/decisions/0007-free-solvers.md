# ADR-0007: The environment ships free solvers only

- **Status:** accepted
- **Date:** 2026-07-16 (recorded 2026-10-24 from the initial design)

## Context

The service's promise is that a canonical payload solves on every framework
from one image, in CI and on a laptop. Commercial solvers (Gurobi, CPLEX)
need licenses that CI cannot hold and reviewers may not have; each framework
also restricts which solvers it can drive.

## Decision

The environment image installs **HiGHS** (`highspy`, drives PyPSA through
linopy), **CBC** (apt `coinor-cbc`, drives both Calliope releases through
pyomo) and **GLPK** (apt `glpk-utils`, the only free solver AdOpT-NET0
supports). The emitters map the canonical `experiment.solver.name` onto what
each framework can use and say so: `highs` is substituted by `cbc` for
Calliope with a warning (Calliope's pyomo backend cannot drive HiGHS; 0.6.8's
pyomo predates the interface), and AdOpT is forced to `glpk`. Typed solver
options (`time_limit`, `mip_gap`, `threads`) are translated to each solver's
own option names where mapped.

## Consequences

- Every scenario in the corpus and every example payload solves to optimal
  in the container with no license; the E2E suite is reproducible anywhere.
- Users with a commercial solver can still name it (`gurobi` is mapped for
  Calliope's options) but must provide it in their own environment.
- Solver behaviour is pinned only loosely: `highspy` is unversioned and
  CBC/GLPK come from apt. Objective pins in E2E carry a relative tolerance
  for that reason.
- Calliope 0.7.0's direct `config.build.backend: highs` (without piecewise
  support) is a future option that would make HiGHS the one solver for two
  frameworks; it is not wired yet.
