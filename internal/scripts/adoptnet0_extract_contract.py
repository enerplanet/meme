#!/usr/bin/env python
"""Extract TEMPO's frozen result contract from a solved AdOpT-NET0 run.

Reads the HDF5 file written by ModelHub.write_results() and emits contract.json
in the shape TEMPO's Results view consumes. Depends only on h5py and numpy.

    python adoptnet0_extract_contract.py <case_dir> <out_contract.json>

Mirrors adoptnet0_runner._extract_results / _to_frozen_contract in the TEMPO repo.
"""
import glob
import json
import os
import sys


def extract(case_dir):
    results_dir = os.path.join(case_dir, "results")
    h5_files = (
        glob.glob(os.path.join(results_dir, "**", "*.h5"), recursive=True)
        or glob.glob(os.path.join(results_dir, "*.h5"))
        or glob.glob(os.path.join(case_dir, "**", "*.h5"), recursive=True)
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
            for key in ("Summary/costs_total", "Summary/Total Cost",
                        "Summary/objective", "Summary/total_cost"):
                if key in f:
                    try:
                        objective = float(np.array(f[key]).ravel()[0])
                        break
                    except Exception:
                        pass
            if objective is None and "Summary" in f:
                for k in f["Summary"]:
                    if "cost" in k.lower() or "objective" in k.lower():
                        try:
                            objective = float(np.array(f[f"Summary/{k}"]).ravel()[0])
                            break
                        except Exception:
                            pass

            design = "Design/nodes/period1"
            if design in f:
                for node in f[design]:
                    raw_cap[node] = {}
                    for tech in f[f"{design}/{node}"]:
                        tech_names.add(tech)
                        try:
                            arr = np.array(f[f"{design}/{node}/{tech}"]).ravel()
                            raw_cap[node][tech] = float(arr[0]) if arr.size == 1 else arr.tolist()
                        except Exception:
                            pass

            op = "Operation/technology_operation/period1"
            if op in f:
                for node in f[op]:
                    raw_dispatch[node] = {}
                    for tech in f[f"{op}/{node}"]:
                        tech_names.add(tech)
                        grp = f[f"{op}/{node}/{tech}"]
                        for out_key in ("output", "Output", "out", "technology_output"):
                            if out_key in grp:
                                try:
                                    raw_dispatch[node][tech] = np.array(grp[out_key]).ravel().tolist()
                                except Exception:
                                    pass
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
        print("usage: adoptnet0_extract_contract.py <case_dir> <out.json>", file=sys.stderr)
        return 2
    result = extract(argv[1])
    with open(argv[2], "w") as fh:
        json.dump(result, fh)
    print(f"contract: objective={result.get('objective')} caps={len(result.get('capacities', {}))}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
