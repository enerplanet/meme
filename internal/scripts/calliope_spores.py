#!/usr/bin/env python
"""SPORES schedule driver for Calliope 0.7 (Lombardi et al. 2020).

Runs under calliope's own interpreter (calliope_run.py picks it):

    python spores_driver.py <model.yaml> <results.nc> <spores_schedule.json> <spores_labels.json>

1. Builds the model (config.init.mode: spores) and solves it: Calliope's native
   loop runs the baseline (cost-optimal) plus the schedule's explore iterations
   with its configured scoring (e.g. relative_deployment).
2. Minimise stages (the paper's Eq. 4): for each target tech group, on the same
   built problem (cost slack still active), the SPORES scores are reset to the
   baseline's, `tempo_excl_score` marks the target and the objective switches to
   `tempo_min_spores` (emitted extra math). The first run per target minimises
   the target only (nos weight 0); later runs add the accumulated SPORES score,
   updated after every run exactly as the native relative scoring does.
3. Writes every SPORE along one `spores` dimension to results.nc plus a labels
   file (stage / target per SPORE) that extract_contract.py reads.
"""
import json
import sys

import pandas as pd
import xarray as xr

import calliope
from calliope import postprocess


def _scores(results, inputs, track, threshold):
    """Relative-deployment score increment: flow_cap / flow_cap_max where above
    the threshold, on tracked techs only (as calliope's own scoring)."""
    ratio = results["flow_cap"] / inputs["flow_cap_max"]
    return ratio.where(ratio > threshold).fillna(0).where(track).fillna(0)


def main(entry, netcdf, schedule_path, labels_path):
    with open(schedule_path, encoding="utf-8") as fh:
        sched = json.load(fh)

    model = calliope.read_yaml(entry)
    model.build()
    model.solve()  # native: baseline + explore iterations
    native = model.results
    parts = [native.sel(spores=s, drop=True) for s in native["spores"].values]
    labels = [{"stage": "cost_optimal" if i == 0 else "explore", "target": None, "iteration": i}
              for i in range(len(parts))]

    inputs = model.inputs
    spores_cfg = model.config.solve.spores
    track = inputs.get(spores_cfg.tracking_parameter, xr.DataArray(True)).notnull() & inputs.definition_matrix
    threshold = spores_cfg.score_threshold_factor
    score0 = _scores(parts[0], inputs, track, threshold)
    weights = sched.get("weights") or {}
    w_excl, w_nos = float(weights.get("excl", 10)), float(weights.get("nos", 1))

    techs = [str(t) for t in inputs["techs"].values]
    backend = model.backend
    minimise = [st for st in sched.get("stages", []) if st.get("type") == "minimise"]
    if minimise:
        backend.set_objective("tempo_min_spores")
    total = sum(len(st.get("targets") or []) * int(st.get("count_each", 0)) for st in minimise)
    done = 0
    for st in minimise:
        for target in st.get("targets") or []:
            members = set(target)
            excl = xr.DataArray([1.0 if t in members else 0.0 for t in techs],
                                coords={"techs": techs}, dims="techs")
            cumulative = score0.copy()
            backend.update_input("tempo_excl_score", excl)
            backend.update_input("tempo_w_excl", w_excl)
            for k in range(int(st.get("count_each", 0))):
                done += 1
                print(f"SPORES minimise {done}/{total}: {'+'.join(target)} (run {k + 1})", flush=True)
                backend.update_input("spores_score", cumulative)
                backend.update_input("tempo_w_nos", 0.0 if k == 0 else w_nos)
                res = backend._solve(model.config.solve, warmstart=False)
                if res.attrs.get("termination_condition") not in ("optimal", "feasible"):
                    print(f"  not optimal ({res.attrs.get('termination_condition')}); skipping the rest of this target", flush=True)
                    break
                res = postprocess.postprocess_model_results(res, model)
                cumulative = cumulative + _scores(res, inputs, track, threshold)
                parts.append(res)
                labels.append({"stage": "minimise", "target": list(target), "iteration": k + 1})

    common = set.intersection(*(set(p.data_vars) for p in parts))
    parts = [p[sorted(common)] for p in parts]
    for p in parts:
        p.attrs = {}
    results = xr.concat(parts, dim=pd.Index(range(len(parts)), name="spores"), combine_attrs="drop")
    model.results = results
    model.to_netcdf(netcdf)
    with open(labels_path, "w", encoding="utf-8") as fh:
        json.dump(labels, fh)
    print(f"SPORES: {len(parts) - 1} alternatives written to {netcdf}", flush=True)


if __name__ == "__main__":
    if len(sys.argv) != 5:
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    main(*sys.argv[1:])
