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
            results["objective"] = float(np.nansum(ds["cost"].values))
        except Exception as e:  # noqa: BLE001
            print(f"  Could not extract objective: {e}", file=sys.stderr)

    # Capacities {node::tech} from flow_cap (max over carriers).
    if "flow_cap" in ds:
        try:
            cap = ds["flow_cap"]
            if "carriers" in cap.dims:
                cap = cap.max(dim="carriers")
            ser = cap.to_series().dropna()
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

    return results


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
