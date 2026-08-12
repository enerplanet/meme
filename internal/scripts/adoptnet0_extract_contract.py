#!/usr/bin/env python
"""Extract TEMPO's frozen result contract from a solved AdOpT-NET0 run.

Reads the HDF5 file written by ModelHub.write_results() and emits contract.json
in the shape TEMPO's Results view consumes. Depends only on h5py and numpy.

    python adoptnet0_extract_contract.py <job_input_dir> <out_contract.json>

<job_input_dir> is the parent of input_data/ (i.e. run_0/input/).
AdOpT writes results into <job_input_dir>/results/<timestamp>/optimization_results.h5.

HDF5 structure (all group names are lowercase):
  summary/lb                          -- optimal objective value
  design/nodes/period1/{node}/{tech}/size  -- installed capacity (MW)
  operation/technology_operation/period1/{node}/{tech}/*_output  -- dispatch timeseries
"""
import glob
import json
import os
import sys


def extract(job_input_dir):
    results_dir = os.path.join(job_input_dir, "results")
    h5_files = (
        glob.glob(os.path.join(results_dir, "**", "*.h5"), recursive=True)
        or glob.glob(os.path.join(results_dir, "*.h5"))
        or glob.glob(os.path.join(job_input_dir, "**", "*.h5"), recursive=True)
    )
    if not h5_files:
        print("no HDF5 file found", file=sys.stderr)
        return {"success": False, "termination_condition": "no_results"}

    h5_path = sorted(h5_files)[-1]
    print(f"reading {os.path.basename(h5_path)}")

    try:
        import h5py
        import numpy as np
    except ImportError as exc:
        print(f"h5py/numpy not available: {exc}", file=sys.stderr)
        return {"success": False, "termination_condition": "extractor_error"}

    objective = None
    raw_cap = {}
    raw_dispatch = {}
    tech_names = set()

    try:
        with h5py.File(h5_path, "r") as f:
            # --- Objective: summary/lb is the optimal LP/MIP lower bound ---
            for key in ("summary/lb", "summary/cost_imports", "summary/total_cost",
                        "Summary/costs_total", "Summary/lb"):
                if key in f:
                    try:
                        objective = float(np.array(f[key]).ravel()[0])
                        break
                    except Exception:
                        pass
            # Fallback: sum all summary/cost_* scalars
            if objective is None and "summary" in f:
                total = 0.0
                found_any = False
                for k in f["summary"]:
                    if "cost" in k.lower():
                        try:
                            total += float(np.array(f[f"summary/{k}"]).ravel()[0])
                            found_any = True
                        except Exception:
                            pass
                if found_any:
                    objective = total

            # --- Capacities: design/nodes/period1/{node}/{tech}/size ---
            for prefix in ("design/nodes/period1", "Design/nodes/period1"):
                if prefix not in f:
                    continue
                for node in f[prefix]:
                    raw_cap[node] = {}
                    for tech in f[f"{prefix}/{node}"]:
                        tech_names.add(tech)
                        grp = f[f"{prefix}/{node}/{tech}"]
                        # 'size' is the installed capacity in MW
                        cap_val = None
                        for cap_key in ("size", "capacity", "cap"):
                            if cap_key in grp:
                                try:
                                    cap_val = float(np.array(grp[cap_key]).ravel()[0])
                                except Exception:
                                    pass
                                break
                        if cap_val is not None:
                            raw_cap[node][tech] = cap_val
                break

            # --- Dispatch: operation/technology_operation/period1/{node}/{tech}/*_output ---
            for prefix in ("operation/technology_operation/period1",
                           "Operation/technology_operation/period1"):
                if prefix not in f:
                    continue
                for node in f[prefix]:
                    raw_dispatch[node] = {}
                    for tech in f[f"{prefix}/{node}"]:
                        tech_names.add(tech)
                        grp = f[f"{prefix}/{node}/{tech}"]
                        # Look for any dataset whose name ends with '_output'
                        for out_key in grp:
                            if out_key.endswith("_output") or out_key in (
                                "output", "Output", "out", "technology_output"
                            ):
                                try:
                                    raw_dispatch[node][tech] = np.array(grp[out_key]).ravel().tolist()
                                except Exception:
                                    pass
                                break
                break

    except Exception as exc:
        print(f"HDF5 read error: {exc}", file=sys.stderr)
        return {"success": False, "termination_condition": "extractor_error"}

    capacities = {
        f"{node}::{tech}": val
        for node, techs in raw_cap.items()
        for tech, val in techs.items()
        if isinstance(val, (int, float))
    }

    dispatch = {}
    dispatch_len = 0
    for techs in raw_dispatch.values():
        for tech, arr in techs.items():
            if not isinstance(arr, list):
                continue
            dispatch_len = max(dispatch_len, len(arr))
            if tech not in dispatch:
                dispatch[tech] = list(arr)
            else:
                for i, v in enumerate(arr):
                    if i < len(dispatch[tech]):
                        dispatch[tech][i] = (dispatch[tech][i] or 0) + (v or 0)
                    else:
                        dispatch[tech].append(v or 0)

    return {
        "success": True,
        "framework": "adoptnet0",
        "termination_condition": "optimal",
        "objective": objective,
        "capacities": capacities,
        "generation": {},
        "dispatch": dispatch,
        "timestamps": list(range(dispatch_len)),
        "transmission_flow": {},
        "demand_timeseries": {},
        "costs_by_tech": {},
        "costs_by_location": {},
        "tech_metadata": {t: {} for t in tech_names},
        "tech_parents": {},
    }


def main(argv):
    if len(argv) != 3:
        print("usage: adoptnet0_extract_contract.py <job_input_dir> <out.json>", file=sys.stderr)
        return 2
    result = extract(argv[1])
    with open(argv[2], "w") as fh:
        json.dump(result, fh)
    print(f"contract: objective={result.get('objective')} caps={len(result.get('capacities', {}))}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
