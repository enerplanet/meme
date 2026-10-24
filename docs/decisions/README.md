# Architecture decisions

Decisions that shape MEME and are not derivable from the code alone are
recorded here as lightweight architecture decision records (ADRs). Each
record states the context, the decision and its consequences, so a later
reader knows why the service looks the way it does before proposing to
change it.

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-go-single-binary.md) | MEME is one dependency-free Go binary; the solvers are external Python processes | accepted |
| [0002](0002-capability-gated-validation.md) | Validation is capability-gated: nothing a payload asks for is dropped without an error or a warning | accepted |
| [0003](0003-native-passthrough.md) | Framework-only concepts travel in per-target `native` blocks, never in the canonical schema | accepted |
| [0004](0004-golden-files-freeze-the-emitter-contract.md) | Golden files and a validation matrix freeze the emitter contract | accepted |
| [0005](0005-async-job-api-and-result-bundle.md) | Simulation is an asynchronous job with a zipped result bundle | accepted |
| [0006](0006-framework-versions-as-targets.md) | Framework versions are separate targets: a default, version variants and aliases | accepted |
| [0007](0007-free-solvers.md) | The environment ships free solvers only: HiGHS, CBC and GLPK | accepted |
| [0008](0008-power-flow-as-a-pypsa-run-mode.md) | Power flow is a PyPSA run mode over an electrical network in the canonical model; MEME never chains jobs | accepted |

## Adding a record

Copy [0000-template.md](0000-template.md) to the next free number, fill it
in, add a row above, and link it from the pull request. A record is never
rewritten once accepted: a factual detail that has aged is corrected in a
dated *Amendments* section at the end of the record (and the status notes
it), and a decision that changes gets a new number and marks the old one
*superseded*.
