# ADR-0005: Simulation is an asynchronous job with a zipped result bundle

- **Status:** accepted
- **Date:** 2026-07-15 (recorded 2026-10-24 from the initial design)

## Context

A solve takes seconds to tens of minutes; a parameter sweep multiplies that.
A synchronous HTTP request of that length is fragile behind proxies, blocks
the client, and cannot report progress. The results are files (netCDF, CSV
trees, HDF5), not JSON.

## Decision

`POST /simulate` validates every requested target synchronously (hard
rejections return `422` immediately) and then schedules one job, answering
`202` with an id. The job emits and runs each target in turn, bounded by a
semaphore across jobs, with every solver process tied to the server's
lifetime context so a shutdown or timeout kills the process group. Clients
poll `GET /jobs/{id}/status` for state and the live console log, and download
`GET /jobs/{id}`: a zip carrying the solver results, the emitted inputs, the
executed driver scripts, the log, and the submitted configuration byte for
byte (API key redacted). `POST /validate` and `POST /convert` stay
synchronous for the fast paths.

## Consequences

- Clients must poll; the per-run results in the status view let them show
  progress and inspect a failing target without waiting for the bundle.
- The bundle is self-describing and reproducible: the native model, the
  script that ran it and the config that produced it are all inside.
- The job store is in memory and jobs are garbage-collected after 24 h; a
  restart loses them. A persistent store is the production follow-up and
  deliberately out of scope here.
- A multi-target job keeps going when one target fails and marks the job
  failed at the end, so the bundle stays as complete as possible.
