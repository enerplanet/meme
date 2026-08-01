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
import os
import shutil
import subprocess
import sys


def _calliope_python():
    """The interpreter that backs the `calliope` CLI — the one with calliope,
    xarray, numpy and pyyaml installed. The driver itself may run under a
    different (system) python that lacks those, so extraction must use this one.
    Falls back to the current interpreter.
    """
    cal = shutil.which("calliope")
    if cal:
        d = os.path.dirname(os.path.realpath(cal))
        for name in ("python", "python3", "python.exe"):
            cand = os.path.join(d, name)
            if os.path.exists(cand):
                return cand
    return sys.executable


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

    # Run the extractor under calliope's own interpreter (it imports calliope to
    # read results.nc). extract_contract.py sits next to this driver.
    here = os.path.dirname(os.path.abspath(__file__))
    extractor = os.path.join(here, "extract_contract.py")
    rc = subprocess.call([_calliope_python(), extractor, entry, netcdf, contract])
    if rc != 0:
        # Never fail a good solve on extraction — the caller surfaces the
        # missing contract instead.
        print("contract extraction failed (non-fatal), rc=%d" % rc, file=sys.stderr)


if __name__ == "__main__":
    _main()
