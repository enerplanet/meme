#!/usr/bin/env python
"""Extract TEMPO's frozen result contract from a solved Calliope 0.7 run.

Depends only on what a Calliope 0.7 environment already provides — calliope and
numpy (NO pyyaml/xarray-direct): all tech metadata (base_tech, carrier, link
endpoints) is sourced from the model that `calliope.read_netcdf` restores, so
nothing beyond the solver env is required. Writes contract.json in the exact
shape TEMPO's Results view consumes.

    python extract_contract.py <results.nc> <out_contract.json>

The contract mirrors python/calliope07_runner.py::_extract_results in the TEMPO
repo — keep the two in sync. The `objective` is Calliope's post-processed
monetary cost (the `cost` result variable), NOT the raw LP objective, which
would include the ensure_feasibility unmet-demand bigM penalty.
"""
import json
import os
import sys

import numpy as np


def _clean(v):
    v = float(v)
    return v if v == v else 0.0  # nan -> 0


def _idx(names, dim):
    return names.index(dim)


def extract(results_nc_path):
    # Calliope 0.7 serialises results into netCDF groups; read_netcdf restores
    # the Model (a bare xarray.open_dataset returns an empty root group). inputs
    # carry base_tech/carrier_out; results hold the optimised variables.
    import calliope

    model = calliope.read_netcdf(results_nc_path)
    inp = model.inputs
    ds = model.results
    # SPORES runs carry a `spores` dimension: the frozen contract describes
    # SPORE 0 (the cost-optimal baseline); every SPORE goes to spores_data.
    spores_ds = None
    if "spores" in ds.dims:
        spores_ds = ds
        ds = ds.sel(spores=ds["spores"].values[0], drop=True)

    # base_tech per tech (dims: techs).
    base_of = {}
    if "base_tech" in inp:
        for tid, v in inp["base_tech"].to_series().dropna().items():
            base_of[str(tid)] = str(v).strip()

    # carrier_out per tech: the first carrier any node outputs (carrier_out is a
    # nodes×techs×carriers boolean membership matrix).
    carrier_out_of = {}
    if "carrier_out" in inp:
        anynode = inp["carrier_out"].any(dim="nodes")  # dims: techs, carriers
        names = list(anynode.to_series().index.names)
        i_t, i_c = names.index("techs"), names.index("carriers")
        for idx, val in anynode.to_series().items():
            tid = str(idx[i_t])
            if val and tid not in carrier_out_of:
                carrier_out_of[tid] = str(idx[i_c]).lower()

    tech_meta = {}
    transmission_tids = set()
    demand_tids = set()
    for tid, base in base_of.items():
        if base == "transmission":
            transmission_tids.add(tid)
        if base == "demand":
            demand_tids.add(tid)
        # display_name/color are intentionally omitted — MEME's model carries no
        # human names, so TEMPO Results fills them from its own model definition
        # rather than being clobbered by a generic id / empty colour.
        tech_meta[tid] = {"parent": base, "carrier_out": carrier_out_of.get(tid, "")}

    # Transmission endpoints: a link tech's flow_cap is defined only at its two
    # end nodes, so the non-null nodes are its endpoints (no link_from/link_to in
    # the netcdf). Direction is irrelevant — net flow sorts the pair.
    link_endpoints = {}  # tid -> (a, b)
    if "flow_cap" in ds and transmission_tids:
        fc = ds["flow_cap"]
        for tid in transmission_tids:
            arr = fc.sel(techs=tid)
            drop = [d for d in arr.dims if d != "nodes"]
            if drop:
                arr = arr.max(dim=drop)
            nodes = [str(n) for n, _ in arr.to_series().dropna().items()]
            if len(nodes) >= 2:
                link_endpoints[tid] = (nodes[0], nodes[1])

    tc = (ds.attrs or {}).get("termination_condition") \
        or (getattr(model, "_model_data", None).attrs.get("termination_condition")
            if getattr(model, "_model_data", None) is not None else None) \
        or "optimal"
    results = {
        "success": True,
        "termination_condition": str(tc),
    }
    if tech_meta:
        results["tech_metadata"] = tech_meta
        results["tech_parents"] = {k: v["parent"] for k, v in tech_meta.items()}

    # Objective — total monetary cost (post-processed; excludes the bigM
    # unmet-demand penalty carried in the raw LP objective).
    if "cost" in ds:
        try:
            results["objective"] = float(np.nansum(_monetary(ds["cost"]).values))
        except Exception as e:  # noqa: BLE001
            print(f"  Could not extract objective: {e}", file=sys.stderr)

    # Capacities {node::tech} from flow_cap (max over carriers).
    if "flow_cap" in ds:
        try:
            ser = _capacity(ds["flow_cap"], inp, base_of).to_series().dropna()
            names = list(ser.index.names)
            i_n, i_t = _idx(names, "nodes"), _idx(names, "techs")
            results["capacities"] = {
                f"{idx[i_n]}::{idx[i_t]}": _clean(v) for idx, v in ser.items()
            }
        except Exception as e:  # noqa: BLE001
            print(f"  Could not extract capacities: {e}", file=sys.stderr)

    # Generation {node::tech::carrier} = flow_out summed over timesteps.
    if "flow_out" in ds:
        try:
            gen = ds["flow_out"].sum(dim="timesteps", min_count=1).to_series().dropna()
            names = list(gen.index.names)
            i_n, i_t, i_c = _idx(names, "nodes"), _idx(names, "techs"), _idx(names, "carriers")
            results["generation"] = {
                f"{idx[i_n]}::{idx[i_t]}::{idx[i_c]}": _clean(v)
                for idx, v in gen.items()
            }
        except Exception as e:  # noqa: BLE001
            print(f"  Could not extract generation: {e}", file=sys.stderr)

    # Dispatch {tech: [hourly]} — flow_out over nodes/carriers, skipping demand
    # and transmission techs.
    if "flow_out" in ds:
        try:
            results["timestamps"] = [str(t) for t in ds["timesteps"].values]
            dispatch = {}
            flow = ds["flow_out"]
            for tid in [str(x) for x in flow["techs"].values]:
                if tid in transmission_tids or tid in demand_tids or "demand" in tid.lower():
                    continue
                arr = flow.sel(techs=tid)
                sum_dims = [d for d in arr.dims if d != "timesteps"]
                vals = arr.sum(dim=sum_dims).values.astype(float)
                vals = np.where(np.isnan(vals), 0.0, vals)
                if vals.sum() > 0:
                    dispatch[tid] = [round(float(v), 3) for v in vals.tolist()]
            results["dispatch"] = dispatch
        except Exception as e:  # noqa: BLE001
            print(f"  Could not extract dispatch: {e}", file=sys.stderr)

    # Transmission flow {a::b: {from, to, timeseries}} — net flow per node pair.
    if "flow_out" in ds and transmission_tids:
        try:
            MAX_TX_PAIRS = 500
            flow = ds["flow_out"]
            flow_techs = {str(x) for x in flow["techs"].values}
            pair_net = {}
            for tid in sorted(transmission_tids & flow_techs):
                n_from, n_to = link_endpoints.get(tid, (None, None))
                if not n_from or not n_to:
                    continue
                arr = flow.sel(techs=tid)
                sum_dims = [d for d in arr.dims if d not in ("timesteps", "nodes")]
                if sum_dims:
                    arr = arr.sum(dim=sum_dims)
                nodes_present = {str(x) for x in arr["nodes"].values}

                def _at(node, _arr=arr, _present=nodes_present):
                    if node not in _present:
                        return None
                    v = _arr.sel(nodes=node).values.astype(float)
                    return np.where(np.isnan(v), 0.0, v)

                delivered_to = _at(n_to)
                delivered_from = _at(n_from)
                if delivered_to is None and delivered_from is None:
                    continue
                n = len(delivered_to if delivered_to is not None else delivered_from)
                a, b = sorted([n_from, n_to])
                to_b = delivered_to if b == n_to else delivered_from
                to_a = delivered_from if b == n_to else delivered_to
                net = (to_b if to_b is not None else np.zeros(n)) \
                    - (to_a if to_a is not None else np.zeros(n))
                key = f"{a}::{b}"
                pair_net[key] = pair_net.get(key, np.zeros(n)) + net

            pair_stats = {k: (v, float(np.abs(v).max()) if len(v) else 0.0)
                          for k, v in pair_net.items()}
            pair_stats = {k: s for k, s in pair_stats.items() if s[1] >= 1e-6}
            if len(pair_stats) > MAX_TX_PAIRS:
                top = sorted(pair_stats, key=lambda k: pair_stats[k][1], reverse=True)[:MAX_TX_PAIRS]
                pair_stats = {k: pair_stats[k] for k in top}
            tx_flow = {}
            for key, (net, _) in pair_stats.items():
                a, b = key.split("::", 1)
                tx_flow[key] = {"from": a, "to": b,
                                "timeseries": [round(float(v), 3) for v in net.tolist()]}
            if tx_flow:
                results["transmission_flow"] = tx_flow
        except Exception as e:  # noqa: BLE001
            print(f"  Could not extract transmission flow: {e}", file=sys.stderr)

    # Demand timeseries — flow_in at demand techs, summed (absolute).
    if "flow_in" in ds and demand_tids:
        try:
            con = ds["flow_in"]
            con_techs = {str(x) for x in con["techs"].values}
            present = sorted(demand_tids & con_techs)
            if present:
                arr = con.sel(techs=present)
                sum_dims = [d for d in arr.dims if d != "timesteps"]
                vals = np.abs(arr).sum(dim=sum_dims).values.astype(float)
                vals = np.where(np.isnan(vals), 0.0, vals)
                results["demand_timeseries"] = [round(float(v), 3) for v in vals.tolist()]
                if "timestamps" not in results:
                    results["timestamps"] = [str(t) for t in con["timesteps"].values]
        except Exception as e:  # noqa: BLE001
            print(f"  Could not extract demand timeseries: {e}", file=sys.stderr)

    # Cost breakdowns {tech: total} and {node: {tech: cost}}.
    if "cost" in ds:
        try:
            cost = ds["cost"]
            if "costs" in cost.dims:
                cost = cost.sum(dim="costs", min_count=1)
            ser = cost.to_series().dropna()
            names = list(ser.index.names)
            i_n, i_t = _idx(names, "nodes"), _idx(names, "techs")
            costs_by_tech = {}
            costs_by_location = {}
            for idx, v in ser.items():
                node, tech = str(idx[i_n]), str(idx[i_t])
                v = _clean(v)
                costs_by_tech[tech] = costs_by_tech.get(tech, 0.0) + v
                costs_by_location.setdefault(node, {})[tech] = \
                    costs_by_location.get(node, {}).get(tech, 0.0) + v
            results["costs_by_tech"] = {k: v for k, v in costs_by_tech.items() if v > 0}
            results["costs_by_location"] = costs_by_location
        except Exception as e:  # noqa: BLE001
            print(f"  Could not extract cost breakdown: {e}", file=sys.stderr)

    if spores_ds is not None:
        try:
            results.update(_spores(spores_ds, inp, base_of, link_endpoints, results_nc_path))
        except Exception as e:  # noqa: BLE001
            print(f"  Could not extract SPORES data: {e}", file=sys.stderr)

    return results


def _monetary(da):
    """The monetary cost class of a costs-indexed array (co2 etc. are not money)."""
    if "costs" in da.dims and "monetary" in [str(c) for c in da["costs"].values]:
        return da.sel(costs="monetary")
    return da


def _capacity(fc, inp, base_of):
    """Installed capacity per node/tech. A conversion's capacity is its INPUT
    flow_cap (MEME's input-referenced capacity; the output flow_cap is a free
    variable) × flow_out_eff, i.e. the output-referenced size TEMPO shows."""
    if "carriers" not in fc.dims:
        return fc
    out = fc.max(dim="carriers")
    conv = [t for t, b in base_of.items() if b == "conversion" and t in fc["techs"].values]
    if conv and "carrier_in" in inp:
        for t in conv:
            is_in = inp["carrier_in"].sel(techs=t).fillna(False).astype(bool)
            f = fc.sel(techs=t).where(is_in).max(dim="carriers")
            eff = 1.0
            if "flow_out_eff" in inp:
                e = inp["flow_out_eff"].sel(techs=t)
                extra = [d for d in e.dims if d != "nodes"]
                eff = (e.max(dim=extra) if extra else e).fillna(1.0)
            out.loc[dict(techs=t)] = f * eff
    return out


def _spores(sds, inp, base_of, link_endpoints, results_nc_path):
    """spores_data / spores_meta in the shape TEMPO's SPORES views consume (as
    TEMPO's Calliope 0.6.8 runner emits). Link techs are keyed 0.6-style,
    'node::<link tech>:<other end>', so TEMPO's metrics recognise the lines."""

    def link_key(node, tid):
        a, b = link_endpoints[tid]
        other = b if node == a else a
        base = tid
        for suffix in (f"_{a}_{b}", f"_{b}_{a}"):
            if tid.endswith(suffix):
                base = tid[: -len(suffix)]
        return f"{node}::{base}:{other}"

    def key(node, tech):
        node, tech = str(node), str(tech)
        return link_key(node, tech) if tech in link_endpoints else f"{node}::{tech}"

    def by_node_tech(da):
        ser = da.to_series().dropna()
        names = list(ser.index.names)
        i_n, i_t = names.index("nodes"), names.index("techs")
        return {key(i[i_n], i[i_t]): _clean(v) for i, v in ser.items() if abs(v) > 1e-9}

    def by_node_tech_carrier(da):
        ser = da.sum(dim="timesteps", min_count=1).to_series().dropna()
        names = list(ser.index.names)
        i_n, i_t, i_c = names.index("nodes"), names.index("techs"), names.index("carriers")
        return {f"{key(i[i_n], i[i_t])}::{i[i_c]}": _clean(v) for i, v in ser.items() if abs(v) > 1e-9}

    slack = float(inp["spores_slack"]) if "spores_slack" in inp else None
    # Stage / target per SPORE, written by the SPORES schedule driver.
    labels = []
    labels_path = os.path.join(os.path.dirname(os.path.abspath(results_nc_path)), "spores_labels.json")
    if os.path.exists(labels_path):
        with open(labels_path, encoding="utf-8") as fh:
            labels = json.load(fh)
    data = []
    for n, s in enumerate(sds["spores"].values):
        d = sds.sel(spores=s, drop=True)
        lab = labels[n] if n < len(labels) else {}
        entry = {
            "spore_id": int(s), "stage": lab.get("stage", "cost_optimal" if n == 0 else "explore"),
            "target": lab.get("target"), "iteration": lab.get("iteration", n), "slack": slack,
            "cost": float(np.nansum(_monetary(d["cost"]).values)) if "cost" in d else None,
        }
        if "flow_cap" in d:
            entry["capacities"] = by_node_tech(_capacity(d["flow_cap"], inp, base_of))
        if "storage_cap" in d:
            entry["storage_capacities"] = by_node_tech(d["storage_cap"])
        if "flow_out" in d:
            entry["generation"] = by_node_tech_carrier(d["flow_out"])
        if "flow_in" in d:
            entry["consumption"] = by_node_tech_carrier(-d["flow_in"])  # 0.6 sign: consumption < 0
        data.append(entry)

    potentials = {}
    if "flow_cap_max" in inp:
        fcm = inp["flow_cap_max"]
        if "carriers" in fcm.dims:
            fcm = _capacity(fcm, inp, base_of)
        potentials["energy_cap_max"] = {k: v for k, v in by_node_tech(fcm).items() if np.isfinite(v)}
    if "storage_cap_max" in inp:
        potentials["storage_cap_max"] = {k: v for k, v in by_node_tech(inp["storage_cap_max"]).items() if np.isfinite(v)}
    hours = float(inp["timestep_resolution"].sum()) if "timestep_resolution" in inp else None
    cost0 = data[0]["cost"] if data else None
    return {
        "spores_data": data,
        "spores_meta": {
            "algorithm": "calliope07_native", "planned": len(data) - 1,
            "cost_optimal": cost0,
            "slacked_cost": cost0 * (1 + slack) if (cost0 is not None and slack is not None) else None,
            "potentials": potentials, "hours": hours,
        },
    }


def main(argv):
    if len(argv) != 3:
        print("usage: extract_contract.py <results.nc> <out.json>", file=sys.stderr)
        return 2
    results_nc_path, out_path = argv[1], argv[2]
    results = extract(results_nc_path)
    with open(out_path, "w", encoding="utf-8") as fh:
        json.dump(results, fh)
    print(f"contract: objective={results.get('objective', 'N/A')} "
          f"techs={len(results.get('tech_metadata', {}))} "
          f"caps={len(results.get('capacities', {}))}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
