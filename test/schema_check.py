#!/usr/bin/env python3
# Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
# SPDX-License-Identifier: MIT

"""Schema/code drift gate (make schema-check).

Validates the canonical payload contract in three steps:

  1. docs/revised_unified_schema.json is a valid JSON Schema (draft 2020-12),
  2. every example payload in examples/ validates against it,
  3. every scenario payload in internal/scenarios/testdata/ (the corpus the Go
     tests and the E2E suite run) validates against it.

The Go module stays dependency-free: runtime payload validation is hand-written
in internal/model, and this script is the CI-time proof that the hand-written
rules and the published schema describe the same contract. A new model field
without a schema entry (or vice versa) fails here instead of drifting.

Requires: python3 with jsonschema >= 4.18 (pip install jsonschema).
"""

import json
import pathlib
import sys

try:
    import jsonschema
    from referencing import Registry, Resource
except ImportError as e:  # pragma: no cover
    sys.exit(f"schema-check needs the python 'jsonschema' package (>= 4.18): {e}")

ROOT = pathlib.Path(__file__).resolve().parent.parent
SCHEMA = ROOT / "docs" / "revised_unified_schema.json"


def main() -> int:
    schema = json.loads(SCHEMA.read_text())
    jsonschema.Draft202012Validator.check_schema(schema)
    print(f"ok  meta-schema: {SCHEMA.relative_to(ROOT)}")

    registry = Registry().with_resources([(schema["$id"], Resource.from_contents(schema))])
    validator = jsonschema.Draft202012Validator(schema, registry=registry)

    payloads = sorted((ROOT / "examples").glob("*.json"))
    scenarios = ROOT / "internal" / "scenarios" / "testdata"
    payloads += sorted((scenarios / "scenarios").glob("*.json"))
    payloads += [scenarios / "sample_payload.json"]

    failed = 0
    for path in payloads:
        doc = json.loads(path.read_text())
        errors = sorted(validator.iter_errors(doc), key=lambda e: list(e.absolute_path))
        if errors:
            failed += 1
            print(f"FAIL {path.relative_to(ROOT)}")
            for err in errors[:5]:
                where = "/".join(map(str, err.absolute_path)) or "<root>"
                print(f"     at {where}: {err.message[:160]}")
        else:
            print(f"ok  {path.relative_to(ROOT)}")

    if failed:
        print(f"\n{failed} payload(s) do not match the schema")
        return 1
    print(f"\nall {len(payloads)} payloads match the schema")
    return 0


if __name__ == "__main__":
    sys.exit(main())
