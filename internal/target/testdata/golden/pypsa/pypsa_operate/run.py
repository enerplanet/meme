import json
CFG = json.loads(r'''{"horizon":12,"input":"${BASE}/input","mga_slack":0.05,"mode":"operate","output":"${BASE}/output","overlap":4,"solver":"highs"}''')
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
    # No optimization: the payload fixed every injection (p_set) and the
    # emitter wrote the AC network (buses with v_nom, lines, transformers).
    # A linear solve first — the result for method "linear", the seed for
    # Newton-Raphson otherwise — then the result tables under power_flow/.
    import math
    import pandas as pd

    PF = CFG["power_flow"]
    pf_dir = os.path.join(OUTPUT, "power_flow")
    os.makedirs(pf_dir, exist_ok=True)
    n_snap = len(n.snapshots)
    converged = pd.Series(False, index=n.snapshots)
    iterations = None
    failure = None
    try:
        n.lpf()
        if PF["method"] == "linear":
            converged[:] = True
        else:
            res = n.pf(use_seed=True, x_tol=PF["tolerance"], distribute_slack=PF["distribute_slack"])
            converged = res["converged"].all(axis=1)
            iterations = res["n_iter"].max(axis=1)
    except Exception as exc:  # a singular Jacobian or a NaN cascade is a failed flow, not a crash
        failure = "%s: %s" % (type(exc).__name__, exc)
        print("power flow aborted:", failure)

    def num(x):
        """A JSON number or None (NaN never reaches summary.json)."""
        if x is None:
            return None
        x = float(x)
        return None if math.isnan(x) or math.isinf(x) else x

    def table(name, df):
        """One result table per quantity: snapshots down, components across."""
        if df is None or df.empty or df.shape[1] == 0:
            return False
        df.to_csv(os.path.join(pf_dir, name), index_label="snapshot")
        return True

    conv = pd.DataFrame({"converged": converged.astype(bool)})
    if iterations is not None:
        conv["iterations"] = iterations.astype(int)
    conv.to_csv(os.path.join(pf_dir, "convergence.csv"), index_label="snapshot")

    # The tables carry every snapshot (a diverged one is worth inspecting);
    # the summary figures come from the converged snapshots only, where the
    # numbers mean something.
    ok_snaps = converged[converged].index

    def peak(df):
        """The largest value over the converged snapshots, or None."""
        df = df.reindex(ok_snaps)
        return num(df.max().max()) if not df.empty and df.shape[1] else None

    def branch_tables(comp, df_t, rating):
        """Flows at both ends, reactive flows when solved, and the loading:
        apparent power over the rating, the larger end counting."""
        p0, p1 = df_t.p0, df_t.p1
        if p0.empty:
            return None
        table(comp + "-p0.csv", p0)
        table(comp + "-p1.csv", p1)
        s0, s1 = p0.abs(), p1.abs()
        if not df_t.q0.empty:
            table(comp + "-q0.csv", df_t.q0)
            table(comp + "-q1.csv", df_t.q1)
            s0 = (p0 ** 2 + df_t.q0 ** 2) ** 0.5
            s1 = (p1 ** 2 + df_t.q1 ** 2) ** 0.5
        load = s0.where(s0 >= s1, s1).div(rating, axis="columns")
        table(comp + "-loading.csv", load)
        return peak(load)

    max_line = branch_tables("lines", n.lines_t, n.lines.s_nom) if len(n.lines) else None
    max_trafo = branch_tables("transformers", n.transformers_t, n.transformers.s_nom) if len(n.transformers) else None
    table("buses-v_mag_pu.csv", n.buses_t.v_mag_pu)
    table("buses-v_ang.csv", n.buses_t.v_ang)
    table("buses-p.csv", n.buses_t.p)
    table("buses-q.csv", n.buses_t.q)
    table("generators-p.csv", n.generators_t.p)
    table("generators-q.csv", n.generators_t.q)
    table("loads-p.csv", n.loads_t.p)
    table("loads-q.csv", n.loads_t.q)
    if len(n.storage_units):
        table("storage_units-p.csv", n.storage_units_t.p)
        table("storage_units-q.csv", n.storage_units_t.q)
    if len(n.links):
        table("links-p0.csv", n.links_t.p0)
        table("links-p1.csv", n.links_t.p1)
    if len(n.shunt_impedances):
        table("shunt_impedances-p.csv", n.shunt_impedances_t.p)
        table("shunt_impedances-q.csv", n.shunt_impedances_t.q)

    ac_buses = n.buses.index[n.buses.carrier == "AC"]
    vmag = n.buses_t.v_mag_pu.reindex(index=ok_snaps, columns=ac_buses) if not n.buses_t.v_mag_pu.empty else None
    ac_sub = n.sub_networks[n.sub_networks.carrier == "AC"] if len(n.sub_networks) else None
    n_ok = int(converged.sum())
    summary = {
        "method": PF["method"],
        "slack": PF["slack"],
        "slack_bus": (ac_sub.slack_bus.iloc[0] if ac_sub is not None and len(ac_sub) else None),
        "snapshots": n_snap,
        "converged_snapshots": n_ok,
        "min_v_mag_pu": num(vmag.min().min()) if vmag is not None and not vmag.empty else None,
        "max_v_mag_pu": num(vmag.max().max()) if vmag is not None and not vmag.empty else None,
        "max_line_loading": max_line,
        "max_transformer_loading": max_trafo,
        "failure": failure,
    }
    with open(os.path.join(pf_dir, "summary.json"), "w") as fh:
        json.dump(summary, fh, indent=2)
    print("power flow converged %d/%d snapshots" % (n_ok, n_snap))
    if n_ok == n_snap and failure is None:
        status, cond = "ok", "power flow converged"
    else:
        status, cond = "failed", "power flow did not converge in %d snapshot(s)" % (n_snap - n_ok)
        if failure:
            cond += " (%s)" % failure
else:
    status, cond = solve_plan()

print("status", status, cond)
n.export_to_netcdf(os.path.join(OUTPUT, "network.nc"))
if MODE != "power_flow":  # a power flow has no objective
    print("objective", n.objective)
sys.exit(0 if status == "ok" else 3)
