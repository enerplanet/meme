# Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
# SPDX-License-Identifier: MIT

import glob, json, os, subprocess, sys

import adopt_net0 as adopt

adopt.copy_technology_data(BASE)
ovp = os.path.join(BASE, "_meme_overrides.json")
ov = json.load(open(ovp)) if os.path.exists(ovp) else {}  # node -> tech -> overrides
for f in glob.glob(os.path.join(BASE, "*", "node_data", "*", "technology_data", "*.json")):
    node = os.path.basename(os.path.dirname(os.path.dirname(f)))
    name = os.path.splitext(os.path.basename(f))[0]
    patch = ov.get(node, {}).get(name)
    if not patch:
        continue
    d = json.load(open(f))
    for k, v in patch.items():
        if isinstance(v, dict) and isinstance(d.get(k), dict):
            d[k].update(v)
        else:
            d[k] = v
    json.dump(d, open(f, "w"))


def merge(dst, src):
    for k, v in src.items():
        if isinstance(v, dict) and isinstance(dst.get(k), dict):
            merge(dst[k], v)
        else:
            dst[k] = v


# Networks: copy the database templates, then apply _meme_network_overrides.json.
nvp = os.path.join(BASE, "_meme_network_overrides.json")
if os.path.exists(nvp):
    adopt.copy_network_data(BASE)
    nov = json.load(open(nvp))  # network -> patch
    for f in glob.glob(os.path.join(BASE, "*", "network_data", "*.json")):
        d = json.load(open(f))
        merge(d, nov.get(os.path.splitext(os.path.basename(f))[0], {}))
        json.dump(d, open(f, "w"))

m = adopt.ModelHub()
m.read_data(BASE)
m.quick_solve()

try:
    m.write_results()
except Exception as exc:
    print(f"write_results failed: {exc}", file=sys.stderr)

# AdOpT writes results into <parent-of-BASE>/results/<timestamp>/, not into BASE/results/.
# Pass the parent directory so the extractor searches the right tree.
here = os.path.dirname(os.path.abspath(__file__))
extractor = os.path.join(here, "adoptnet0_extract_contract.py")
rc = subprocess.call([sys.executable, extractor, os.path.dirname(BASE), CONTRACT])
if rc != 0:
    print(f"contract extraction failed (rc={rc})", file=sys.stderr)
