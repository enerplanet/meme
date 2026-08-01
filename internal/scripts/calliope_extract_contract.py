#!/usr/bin/env python
"""Extract TEMPO's frozen result contract from a solved Calliope 0.7 run.

Standalone, dependency-light (xarray + numpy + pyyaml — all present in a
Calliope 0.7 environment). Reads the emitted model.yaml (for tech metadata:
base_tech, link endpoints, carrier, name, color) and the solved results.nc
(for the optimised variables), and writes contract.json in the exact shape
TEMPO's Results view consumes.

    python extract_contract.py <model.yaml> <results.nc> <out_contract.json>

The contract mirrors python/calliope07_runner.py::_extract_results in the TEMPO
repo — keep the two in sync. The `objective` is Calliope's post-processed
monetary cost (the `cost` result variable), NOT the raw LP objective, which
would include the ensure_feasibility unmet-demand bigM penalty.
"""
import json
import sys

import numpy as np
import yaml


def _clean(v):
    v = float(v)
    return v if v == v else 0.0  # nan -> 0


def _idx(names, dim):
    return names.index(dim)


def extract(model_yaml_path, results_nc_path):
    with open(model_yaml_path, "r", encoding="utf-8") as fh:
        doc = yaml.safe_load(fh) or {}
    techs = doc.get("techs") or {}

    # Tech metadata sourced from the emitted YAML. MEME does not emit name/color,
    # so those fall back to the tech id (TEMPO Results supplies defaults).
    tech_meta = {}
    transmission_tids = set()
    demand_tids = set()
    link_endpoints = {}  # tid -> (from, to)
    for tid, tdef in techs.items():
        tdef = tdef or {}
        base = str(tdef.get("base_tech", "")).strip()
        if base == "transmission":
            transmission_tids.add(tid)
        if base == "demand":
            demand_tids.add(tid)
        lf, lt = tdef.get("link_from"), tdef.get("link_to")
        if lf and lt:
            link_endpoints[tid] = (str(lf), str(lt))
        carrier_out = tdef.get("carrier_out") or tdef.get("carrier") or ""
        if isinstance(carrier_out, list):
            carrier_out = carrier_out[0] if carrier_out else ""
        meta = {"parent": base, "carrier_out": str(carrier_out).strip().lower()}
        # Only emit display_name/color when the emitted YAML actually carries
        # them. MEME does not, so these are omitted and the consumer (TEMPO
        # Results) fills them from its own model definition rather than being
        # clobbered by a generic id / empty colour.
        name = tdef.get("name")
        if name:
            meta["display_name"] = str(name)
        color = tdef.get("color")
        if color:
            meta["color"] = str(color)
        tech_meta[tid] = meta

    # Calliope 0.7 serialises results into netCDF groups; read_netcdf restores
    # the Model, whose .results holds the optimised variables (a bare
    # xarray.open_dataset returns an empty root group).
    import calliope

    model = calliope.read_netcdf(results_nc_path)
    ds = model.results

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
    if len(argv) != 4:
        print("usage: extract_contract.py <model.yaml> <results.nc> <out.json>",
              file=sys.stderr)
        return 2
    model_yaml_path, results_nc_path, out_path = argv[1], argv[2], argv[3]
    results = extract(model_yaml_path, results_nc_path)
    with open(out_path, "w", encoding="utf-8") as fh:
        json.dump(results, fh)
    print(f"contract: objective={results.get('objective', 'N/A')} "
          f"techs={len(results.get('tech_metadata', {}))} "
          f"caps={len(results.get('capacities', {}))}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
