CFG = {"horizon":24,"input":"${BASE}/input","mga_slack":0.1,"mode":"alternatives","output":"${BASE}/output","overlap":0,"solver":"highs"}
# Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
# SPDX-License-Identifier: MIT

import json, os, sys

import pypsa

INPUT, OUTPUT = CFG["input"], CFG["output"]
# Solver options (time limit, MIP gap, threads, ...) already mapped onto the
# selected solver's own option names by the emitter (pypsa.solverOptions).
SOLVER_OPTS = CFG.get("solver_options") or {}

n = pypsa.Network()
n.import_from_csv_folder(INPUT)


def match_names(index, techs, prefixes=True):
    """Component names matching canonical tech ids (id or id@node)."""
    out = []
    for name in index:
        base = str(name).split("@", 1)[0]
        if base in techs:
            out.append(name)
    return out


def term_expr(m, t):
    """One constraint term as a linopy expression, or None if nothing matches."""
    coeff = float(t.get("coefficient", 1))
    techs = t.get("techs") or []
    nodes = t.get("nodes") or []
    carriers = t.get("carriers") or []
    total = None
    for comp, vname in (("generators", "Generator"), ("links", "Link"), ("storage_units", "StorageUnit")):
        df = getattr(n, comp)
        if df.empty:
            continue
        names = match_names(df.index, techs) if techs else list(df.index)
        if nodes:
            buscol = "bus0" if comp == "links" else "bus"
            names = [x for x in names if str(df.loc[x, buscol]).split("::")[0] in nodes]
        if carriers and "carrier" in df.columns:
            names = [x for x in names if df.loc[x, "carrier"] in carriers]
        if not names:
            continue
        var = t["variable"]
        if var == "capacity":
            key = vname + "-p_nom"
            if key not in m.variables:
                continue
            v = m.variables[key]
            names = [x for x in names if x in v.indexes["name"]]
            if not names:
                continue
            e = v.sel(name=names).sum() * coeff
        elif var in ("flow_out", "flow_in"):
            key = vname + "-p"
            if key not in m.variables:
                continue
            v = m.variables[key]
            names = [x for x in names if x in v.indexes["name"]]
            if not names:
                continue
            e = v.sel(name=names).sum() * coeff
        elif var == "storage_cap":
            if vname != "StorageUnit" or "StorageUnit-p_nom" not in m.variables:
                continue
            v = m.variables["StorageUnit-p_nom"]
            e = None
            for x in names:
                if x not in v.indexes["name"]:
                    continue
                part = v.sel(name=[x]).sum() * (coeff * float(df.loc[x, "max_hours"]))
                e = part if e is None else e + part
            if e is None:
                continue
        else:
            raise SystemExit("unsupported constraint variable %r" % var)
        total = e if total is None else total + e
    return total


def apply_constraints(m):
    path = os.path.join(INPUT, "_constraints.json")
    if not os.path.exists(path):
        return
    for c in json.load(open(path))["constraints"]:
        expr = None
        for t in c["terms"]:
            e = term_expr(m, t)
            if e is None:
                raise SystemExit("constraint %r: no matching components for term %r" % (c.get("name"), t))
            expr = e if expr is None else expr + e
        sense = "=" if c["sense"] == "==" else c["sense"]
        m.add_constraints(expr, sense, float(c["bound"]), name="meme-" + (c.get("name") or "constraint"))
        print("applied sidecar constraint", c.get("name"))


def solve_plan():
    m = n.optimize.create_model()
    apply_constraints(m)
    return n.optimize.solve_model(solver_name=CFG["solver"], **SOLVER_OPTS)


MODE = CFG["mode"]
if MODE == "stochastic":
    spec = json.load(open(os.path.join(INPUT, "_scenarios.json")))
    n.set_scenarios({s["name"]: s["weight"] for s in spec["scenarios"]})
    for s in spec["scenarios"]:
        for ov in s.get("sets", []):
            df = getattr(n, ov["table"])
            comps = [x for x in df.index.get_level_values("name").unique()
                     if x == ov["component"] or str(x).startswith(ov["component"] + "@")]
            if not comps:
                raise SystemExit("scenario %r: no component matches %r" % (s["name"], ov["component"]))
            for x in comps:
                df.loc[(s["name"], x), ov["attribute"]] = ov["value"]
    status, cond = n.optimize(solver_name=CFG["solver"], **SOLVER_OPTS)
elif MODE == "operate":
    n.optimize.optimize_with_rolling_horizon(
        horizon=CFG["horizon"], overlap=CFG["overlap"], solver_name=CFG["solver"], **SOLVER_OPTS)
    status, cond = "ok", "rolling-horizon complete"
elif MODE == "alternatives":
    status, cond = solve_plan()
    print("status", status, cond)
    if status == "ok":
        n.export_to_netcdf(os.path.join(OUTPUT, "network_baseline.nc"))
        print("baseline objective", n.objective)
        ext = n.generators.index[n.generators.p_nom_extendable]
        weights = {"Generator": {"p_nom": {x: 1.0 for x in ext}}}
        status, cond = n.optimize.optimize_mga(slack=CFG["mga_slack"], weights=weights, **SOLVER_OPTS)
elif MODE == "power_flow":
    import math
    import pandas as pd

    # csv_sim_<name>/<pyssa-exported p0/p1/v_mag_pu...> — EnerPlanET's reader
    # walks for a dir prefixed csv_sim_ under the output root.
    sim = os.path.join(OUTPUT, "csv_sim_" + (n.name or "model"))
    os.makedirs(sim, exist_ok=True)

    snapshots = list(n.snapshots)
    # Per-snapshot pf(); lpf() seeds the non-linear solve. Results are clean
    # only when EVERY snapshot converges; a non-converged solve clears the
    # electrical exports (the legacy convergence guard) but is a NORMAL
    # outcome — we still write convergence_stats and exit 0.
    n.lpf()
    try:
        full = n.pf(use_seed=True)
    except Exception as exc:
        print("power flow failed:", exc)
        full = None

    converged = [False] * len(snapshots)
    if full is not None and "converged" in full:
        cv = full["converged"]
        vals = cv.values.flatten() if hasattr(cv, "values") else list(cv)
        converged = [bool(v) for v in vals[:len(snapshots)]]
        # pad in case pf skipped snapshots
        converged += [False] * (len(snapshots) - len(converged))

    n_converged = sum(converged)
    pd.DataFrame({
        "snapshots": snapshots,
        "converged": converged,
        "n_iter": [int(getattr(full, "iteration", 0)) if full is not None else 0] * len(snapshots),
    }).to_csv(os.path.join(OUTPUT, "convergence_stats.csv"), index=False)
    print("power flow converged: %d/%d snapshots" % (n_converged, len(snapshots)))

    if n_converged == len(snapshots) and len(snapshots) > 0 and full is not None:
        print("exporting power flow results")
        n.export_to_netcdf(os.path.join(OUTPUT, "network.nc"))
        # Web PF CSVs in the EnerPlanET layout: first column is the timestep,
        # then one column per bus/line/transformer. Rows are snapshots.
        def _web(name, df):
            cols = list(df.columns)
            header = ["timesteps"] + cols
            rows = [header]
            for i, snap in enumerate(snapshots):
                row = [str(snap)] + [str(df.iloc[i][c]) for c in cols]
                rows.append(row)
            df_out = pd.DataFrame(rows[1:], columns=rows[0])
            df_out.to_csv(os.path.join(sim, name), index=False)

        if "v_mag_pu" in n.buses_t:
            _web("buses-v_mag_pu.csv", n.buses_t["v_mag_pu"])
        if "p" in n.buses_t:
            _web("buses-p.csv", n.buses_t["p"])
        if "q" in n.buses_t:
            _web("buses-q.csv", n.buses_t["q"])
        if len(n.lines) > 0 and "p0" in n.lines_t:
            _web("lines-p0.csv", n.lines_t["p0"])
            _web("lines-p1.csv", n.lines_t["p1"])
        if len(n.transformers) > 0 and "p0" in n.transformers_t:
            _web("transformers-p0.csv", n.transformers_t["p0"])
            _web("transformers-p1.csv", n.transformers_t["p1"])
    else:
        print("power flow did not converge; electrical exports cleared (normal)")
    status, cond = ("ok", "power-flow complete")
else:
    status, cond = solve_plan()

print("status", status, cond)
# The power_flow branch already exports network.nc (on convergence) on its own;
# it has no optimization objective (pf() is not a solve), so skip the dispatch
# tail for it. All other modes keep the normal export.
if MODE != "power_flow":
    n.export_to_netcdf(os.path.join(OUTPUT, "network.nc"))
    print("objective", n.objective)
sys.exit(0 if status == "ok" else 3)
