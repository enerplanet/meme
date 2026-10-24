# Attributions

MEME itself is written in Go against the standard library only and carries no
third-party Go dependencies. The frameworks and solvers below are **not part of
this repository's code**: the `environment/` image installs them, and the
service drives them as external processes. Each remains under its own license.

## Energy-system frameworks (translation targets)

| Project | Version pinned | License | Role |
|---|---|---|---|
| [PyPSA](https://pypsa.org) | 1.2.4 | MIT | target `pypsa`; driven through a generated `run.py` using [linopy](https://github.com/PyPSA/linopy) (MIT) |
| [Calliope](https://www.callio.pe) | 0.7.0 | Apache-2.0 | target `calliope` (alias `calliope-0.7`); driven through the `calliope` CLI |
| [Calliope](https://www.callio.pe) | 0.6.8 | Apache-2.0 | target `calliope-0.6`; driven through the `calliope-0.6` CLI |
| [AdOpT-NET0](https://github.com/UU-ER/AdOpT-NET0) | 0.1.10 | MIT | target `adopt-net0`; driven through a generated `run.py` |

The JSON Schemas under `schemas/` describe these frameworks' input formats
and were derived from their published documentation and machine-readable
schemas; see each file's description for its provenance.

## Solvers (free and open source)

| Project | Source in the image | License | Used by |
|---|---|---|---|
| [HiGHS](https://highs.dev) | `highspy` (PyPI) | MIT | PyPSA |
| [COIN-OR CBC](https://github.com/coin-or/Cbc) | Debian `coinor-cbc` | EPL-2.0 | Calliope 0.7 and 0.6 (through Pyomo) |
| [GLPK](https://www.gnu.org/software/glpk/) | Debian `glpk-utils` | GPL-3.0-or-later | AdOpT-NET0 (invoked as a separate `glpsol` process) |
| [Pyomo](https://www.pyomo.org) | PyPI | BSD-3-Clause | solver interface of both Calliope releases |

## Tooling

| Project | License | Role |
|---|---|---|
| [uv](https://github.com/astral-sh/uv) | MIT or Apache-2.0 | provisions the Calliope virtual environments and the Python 3.8 interpreter in the image |
| [MkDocs](https://www.mkdocs.org) and [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/) | BSD-2-Clause / MIT | the documentation site |

## Repository template

The repository was created from the THD-Spatial-AI open-source template, which
provides the license, contributing and code-of-conduct scaffolding, the
branch and commit conventions, and the documentation site setup.
