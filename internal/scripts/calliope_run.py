"""Driver: solve a Calliope 0.7 model via the CLI, then extract TEMPO's frozen
result contract into contract.json.

The emitter (calliope.Plan) prepends a CFG dict with the entrypoint, netCDF/CSV
output paths, and the contract path, then writes this alongside
extract_contract.py into the run directory. Invoked as `python run.py`.

The solve step is kept identical to MEME's bare `calliope run` invocation so its
console output (which the job log streams) is unchanged. Contract extraction is
best-effort: a solved run is never marked failed just because extraction hit a
snag — the missing contract.json is surfaced by the caller instead.
"""
import json
import os
import subprocess
import sys
import traceback


def _main():
    entry = CFG["entrypoint"]
    netcdf = CFG["netcdf"]
    csv = CFG["csv"]
    contract = CFG["contract"]

    rc = subprocess.call(
        ["calliope", "run", entry, "--save_netcdf", netcdf, "--save_csv", csv]
    )
    if rc != 0:
        sys.exit(rc)

    # extract_contract.py is written into the same directory as this driver.
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    try:
        import extract_contract

        data = extract_contract.extract(entry, netcdf)
        with open(contract, "w", encoding="utf-8") as fh:
            json.dump(data, fh)
        print("contract written: %s (objective=%s)"
              % (contract, data.get("objective", "N/A")))
    except Exception:  # noqa: BLE001 — never fail a good solve on extraction
        print("contract extraction failed (non-fatal):", file=sys.stderr)
        traceback.print_exc()


if __name__ == "__main__":
    _main()
