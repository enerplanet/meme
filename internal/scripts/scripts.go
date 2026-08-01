// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package scripts holds the Python driver scripts written into each job's run
// directory at plan time. They live in this folder so the Python
// side of every target is in one place; go:embed cannot cross package
// directories, so this package embeds them and the target packages import the
// exported bodies.
package scripts

import _ "embed"

// PyPSARun drives PyPSA: import the CSV folder, apply the _constraints.json
// sidecar as linopy constraints, branch on the experiment mode, export the
// solved network. The emitter prepends a CFG line (see pypsa.runPy).
//
//go:embed pypsa_run.py
var PyPSARun string

// AdOptNET0Run drives AdOpT-NET0: copy the mapped database technologies into
// the input tree, patch them with the emitter's node-keyed overrides, then
// solve via ModelHub().quick_solve(). The emitter prepends a BASE line.
//
//go:embed adoptnet0_run.py
var AdOptNET0Run string

// CalliopeRun drives Calliope 0.7: run the CLI solve (save_netcdf + save_csv),
// then extract TEMPO's frozen result contract into contract.json. The emitter
// prepends a CFG line (see calliope.Plan) and writes CalliopeExtractContract
// alongside it as extract_contract.py.
//
//go:embed calliope_run.py
var CalliopeRun string

// CalliopeExtractContract reads the emitted model.yaml (tech metadata) and the
// solved results.nc (via calliope.read_netcdf) and emits contract.json in
// TEMPO's frozen result shape. Written into the run dir as extract_contract.py
// and imported by CalliopeRun. Mirrors calliope07_runner._extract_results in
// the TEMPO repo.
//
//go:embed calliope_extract_contract.py
var CalliopeExtractContract string
