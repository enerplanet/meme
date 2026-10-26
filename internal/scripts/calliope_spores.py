#!/usr/bin/env python
"""SPORES schedule driver for Calliope 0.7 (Lombardi et al. 2020).

Runs under calliope's own interpreter (calliope_run.py picks it):

    python spores_driver.py <model.yaml> <results.nc> <spores_schedule.json> <spores_labels.json>

With `solver: highs` every solve (baseline, explore, minimise) goes through the
HiGHS bridge (highs_solver.py, written alongside this driver).

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

Each minimise target depends only on the baseline and its own earlier runs, so
with `solver: highs` the targets run in forked worker processes alongside the
explore iterations (see _Pool). solver_options `spores_workers` sets how many:
"auto" (the default) sizes the pool from the memory available when the LP is
loaded, N fixes it, 1 runs the targets serially after the explore iterations.

The explore iterations form one chain (each scores all earlier ones). With
solver_options `spores_explore_chains: K` (K > 1) they are split into K
independent chains of about equal length instead, which run in parallel too:
chain 0 is Calliope's native loop (shortened); chain c > 0 starts from the
baseline scores plus a reproducible random score (uniform 0..1, seed c) on
every tracked node/tech, so the chains head in different directions, and then
accumulates its own SPOREs' scores exactly as the native relative-deployment
scoring does. Their SPOREs are labelled with their chain. This changes the
method: a chain only avoids its own earlier SPOREs, not the other chains'.
"""
import ctypes
import json
import os
import pickle
import shutil
import signal
import sys
import traceback

import numpy as np
import pandas as pd
import xarray as xr

import calliope
from calliope import postprocess


def _scores(results, inputs, track, threshold):
    """Relative-deployment score increment: flow_cap / flow_cap_max where above
    the threshold, on tracked techs only (as calliope's own scoring)."""
    ratio = results["flow_cap"] / inputs["flow_cap_max"]
    return ratio.where(ratio > threshold).fillna(0).where(track).fillna(0)


class _Family:
    """One minimise target: `count` runs, each from the baseline scores plus
    its own earlier runs (independent of every other target)."""

    kind = "minimise"

    def __init__(self, index, target, count):
        self.index, self.target, self.count = index, list(target), count

    def part(self, k):
        return f"part_m{self.index:04d}_{k:04d}.nc"


class _Chain:
    """One independent explore chain (spores_explore_chains); chain 0 is the
    native loop, `first` the global explore iteration of its first SPORE."""

    kind = "explore"
    target = None

    def __init__(self, index, count, first):
        self.index, self.count, self.first = index, count, first

    def part(self, k):
        return f"part_e{self.index:04d}_{k:04d}.nc"


class _Ctx:
    """What a minimise run needs from the built model."""

    def __init__(self, model, sched):
        self.model = model
        self.inputs = model.inputs
        spores_cfg = model.config.solve.spores
        self.track = (self.inputs.get(spores_cfg.tracking_parameter, xr.DataArray(True)).notnull()
                      & self.inputs.definition_matrix)
        self.threshold = spores_cfg.score_threshold_factor
        weights = sched.get("weights") or {}
        self.w_excl, self.w_nos = float(weights.get("excl", 10)), float(weights.get("nos", 1))
        self.techs = [str(t) for t in self.inputs["techs"].values]

    def scores(self, results):
        return _scores(results, self.inputs, self.track, self.threshold)

    def run_family(self, fam, score0, progress):
        """Solve one family; yields (iteration, postprocessed results) as each
        run finishes, so a worker writes it before starting the next."""
        backend = self.model.backend
        members = set(fam.target)
        excl = xr.DataArray([1.0 if t in members else 0.0 for t in self.techs],
                            coords={"techs": self.techs}, dims="techs")
        cumulative = score0.copy()
        backend.update_input("tempo_excl_score", excl)
        backend.update_input("tempo_w_excl", self.w_excl)
        for k in range(fam.count):
            print(f"SPORES minimise {progress(k)}: {'+'.join(fam.target)} (run {k + 1})", flush=True)
            backend.update_input("spores_score", cumulative)
            backend.update_input("tempo_w_nos", 0.0 if k == 0 else self.w_nos)
            res = backend._solve(self.model.config.solve, warmstart=False)
            if res.attrs.get("termination_condition") not in ("optimal", "feasible"):
                print(f"  not optimal ({res.attrs.get('termination_condition')}); skipping the rest of this target", flush=True)
                break
            res = postprocess.postprocess_model_results(res, self.model)
            cumulative = cumulative + self.scores(res)
            yield k + 1, res

    def run_chain(self, chain, score0, progress):
        """Solve one independent explore chain (objective min_spores, which
        the caller sets); yields (iteration within the chain, results) as each
        run finishes."""
        backend = self.model.backend
        rng = np.random.default_rng(chain.index)
        seed = xr.zeros_like(score0) + rng.uniform(0.0, 1.0, size=score0.shape)
        cumulative = score0 + seed.where(self.track).fillna(0)
        for k in range(chain.count):
            print(f"SPORES explore {progress(k)}: chain {chain.index} (run {k + 1})", flush=True)
            backend.update_input("spores_score", cumulative)
            res = backend._solve(self.model.config.solve, warmstart=False)
            if res.attrs.get("termination_condition") not in ("optimal", "feasible"):
                print(f"  not optimal ({res.attrs.get('termination_condition')}); stopping this chain", flush=True)
                break
            res = postprocess.postprocess_model_results(res, self.model)
            cumulative = cumulative + self.scores(res)
            yield k + 1, res

    def run(self, job, score0, progress):
        """Run a minimise target or an explore chain under its objective."""
        if job.kind == "explore":
            self.model.backend.set_objective("min_spores")
            return self.run_chain(job, score0, progress)
        self.model.backend.set_objective("tempo_min_spores")
        return self.run_family(job, score0, progress)


def _label(job, k):
    if job.kind == "explore":
        return {"stage": "explore", "target": None, "iteration": job.first + k - 1, "chain": job.index}
    return {"stage": "minimise", "target": job.target, "iteration": k}


def _meminfo(key):
    with open("/proc/meminfo", encoding="ascii") as fh:
        for line in fh:
            if line.startswith(key + ":"):
                return int(line.split()[1]) * 1024
    return 0


def _rss():
    with open("/proc/self/status", encoding="ascii") as fh:
        for line in fh:
            if line.startswith("VmRSS:"):
                return int(line.split()[1]) * 1024
    return 0


def _available_memory():
    """MemAvailable, capped by a cgroup v2 memory limit if the container has one."""
    avail = _meminfo("MemAvailable")
    try:
        with open("/sys/fs/cgroup/memory.max", encoding="ascii") as fh:
            limit = fh.read().strip()
        if limit != "max":
            with open("/sys/fs/cgroup/memory.current", encoding="ascii") as fh:
                avail = min(avail, int(limit) - int(fh.read()))
    except (OSError, ValueError):
        pass
    return avail


def _auto_workers(jobs):
    """Workers that fit in memory, at the moment the LP is loaded. A worker's
    private memory is what it writes after the fork (HiGHS workspace, Calliope
    results); it stays below the parent's resident size at that point
    (measured: week 0.5-0.9 GiB per worker vs 2.6 GiB parent; full year: a
    HiPO solve peaks at ~11 GiB vs ~42 GiB parent), so that size is the
    per-worker budget, with a quarter of it kept for the parent's own solves
    and a tenth of the available memory spare."""
    rss, avail = _rss(), _available_memory()
    per = max(rss, 2**30)
    by_memory = int((0.9 * avail - 0.25 * rss) // per)
    by_cpu = max(1, (os.cpu_count() or 1) - 1)
    n = max(1, min(jobs, by_memory, by_cpu))
    return n, (f"auto: {avail / 2**30:.0f} GiB available, parent {rss / 2**30:.1f} GiB resident "
               f"-> room for {by_memory}, {by_cpu} spare core(s), {jobs} target(s)/chain(s)")


class _Pool:
    """Minimise targets and explore chains 1..K-1 in forked worker processes,
    alongside the parent's native explore loop.

    The workers fork right after the bridge loads the LP and before HiGHS first
    runs (so no solver threads exist yet); each inherits the built model and
    the loaded HiGHS instance copy-on-write. After the baseline solve the parent
    sends them the baseline scores and cost, then carries on with Calliope's
    native explore iterations while they solve their targets. Each worker
    writes every SPORE to <results dir>/spores_parts/ as soon as it is solved,
    and the parent merges them in schedule order, so the output has the same
    layout as a serial run."""

    def __init__(self, ctx, jobs, workers, part_dir):
        self.ctx, self.part_dir = ctx, part_dir
        self.jobs = jobs  # _Chain and _Family, in output order
        self.requested = workers  # an int, or "auto" (sized in fork)
        self.workers = 0
        self.assign = []
        self.children = []  # (pid, write end of its pipe)
        self.sent = False

    def _plan(self):
        how = ""
        if self.requested == "auto":
            self.workers, how = _auto_workers(len(self.jobs))
        else:
            self.workers = min(self.requested, len(self.jobs))
        # longest first, each to the least-loaded worker
        self.assign = [[] for _ in range(self.workers)]
        load = [0] * self.workers
        for job in sorted(self.jobs, key=lambda j: -j.count):
            i = load.index(min(load))
            self.assign[i].append(job)
            load[i] += job.count
        return how

    def fork(self, backend):
        import highs_solver
        highs_solver.HOOKS["on_loaded"] = None
        how = self._plan()
        os.makedirs(self.part_dir, exist_ok=True)
        threads = max(1, (os.cpu_count() or 1) // (self.workers + 1))
        for i, fams in enumerate(self.assign):
            r, w = os.pipe()
            sys.stdout.flush()
            sys.stderr.flush()
            pid = os.fork()
            if pid == 0:
                os.close(w)
                for _, fd in self.children:
                    os.close(fd)
                code = 1
                try:
                    self._worker(i, fams, r, threads)
                    code = 0
                except BaseException:
                    traceback.print_exc()
                finally:
                    sys.stdout.flush()
                    sys.stderr.flush()
                    os._exit(code)  # never return into the parent's solve
            os.close(r)
            self.children.append((pid, w))
        chains = sum(j.kind == "explore" for j in self.jobs)
        print(f"SPORES: {len(self.jobs) - chains} minimise target(s)"
              + (f" and {chains} explore chain(s)" if chains else "") + f" on {self.workers} worker process(es), "
              f"{threads} HiGHS thread(s) each" + (f" ({how})" if how else ""), flush=True)

    def baseline(self, backend, results):
        import highs_solver
        highs_solver.HOOKS["on_solved"] = None
        model = self.ctx.model
        score0 = self.ctx.scores(postprocess.postprocess_model_results(results.copy(), model))
        cost = float(results[model.config.build.objective])
        data = pickle.dumps((score0, cost), protocol=pickle.HIGHEST_PROTOCOL)
        for _, w in self.children:
            with os.fdopen(w, "wb") as fh:
                try:
                    fh.write(data)
                except BrokenPipeError:
                    pass
        self.sent = True

    def _worker(self, i, jobs, rfd, threads):
        import highs_solver
        try:  # die with the parent
            ctypes.CDLL("libc.so.6").prctl(1, signal.SIGKILL)  # PR_SET_PDEATHSIG
        except (OSError, AttributeError):
            pass
        highs_solver.HOOKS["on_loaded"] = highs_solver.HOOKS["on_solved"] = None
        highs_solver._LABEL = f"[w{i}] "
        highs_solver.OVERRIDES["threads"] = threads
        log = os.path.join(self.part_dir, f"highs_w{i}.log")
        highs_solver._LOG_FILE = log
        backend = self.ctx.model.backend
        backend._meme_highs.dump = False  # keep_lp: the parent keeps the LP
        h = backend._meme_highs.h
        h.setOptionValue("log_to_console", False)
        h.setOptionValue("log_file", log)
        with os.fdopen(rfd, "rb") as fh:
            data = fh.read()
        if not data:
            return  # the baseline did not solve: nothing to do
        score0, cost = pickle.loads(data)
        backend.update_input("spores_baseline_cost", cost)
        for job in jobs:
            for k, res in self.ctx.run(job, score0, lambda k: f"[w{i}] {job.kind} job {job.index}"):
                res.attrs = {}
                # each run on disk as soon as it is solved (a stopped job keeps
                # them); written aside and renamed, so a part is never half
                # written. Compressed like Calliope's own files: a full-year 3H
                # SPORE is 1.1 GB raw, ~32 MB compressed.
                path = os.path.join(self.part_dir, job.part(k))
                enc = {v: {"zlib": True, "complevel": 4} for v in res.data_vars if res[v].dtype.kind in "biuf"}
                res.to_netcdf(path + ".tmp", format="NETCDF4", encoding=enc)
                os.replace(path + ".tmp", path)

    def collect(self):
        """Wait for the workers; returns their SPOREs and labels in schedule order."""
        if not self.sent:
            for _, w in self.children:  # the baseline never solved: release them
                os.close(w)
        failed = []
        for pid, _ in self.children:
            _, status = os.waitpid(pid, 0)
            if os.waitstatus_to_exitcode(status) != 0:
                failed.append(os.waitstatus_to_exitcode(status))
        if failed:
            raise RuntimeError(f"{len(failed)} SPORES worker process(es) failed (exit codes {failed}); see the log")
        parts, labels = [], []
        for job in self.jobs:
            for k in range(1, job.count + 1):
                path = os.path.join(self.part_dir, job.part(k))
                if not os.path.exists(path):
                    break  # the chain/target stopped early (non-optimal run)
                with xr.open_dataset(path) as ds:
                    parts.append(ds.load())
                labels.append(_label(job, k))
        out_dir = os.path.dirname(self.part_dir)
        for i in range(self.workers):  # keep the workers' HiGHS logs next to highs.log
            log = os.path.join(self.part_dir, f"highs_w{i}.log")
            if os.path.exists(log):
                shutil.move(log, os.path.join(out_dir, f"highs_w{i}.log"))
        shutil.rmtree(self.part_dir, ignore_errors=True)
        return parts, labels


def main(entry, netcdf, schedule_path, labels_path):
    with open(schedule_path, encoding="utf-8") as fh:
        sched = json.load(fh)

    calliope.set_log_verbosity("info", include_solver_output=True)
    model = calliope.read_yaml(entry)
    highs = model.config.solve.solver == "highs"
    if highs:
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        import highs_solver
        highs_solver.install(os.path.dirname(os.path.abspath(netcdf)))

    minimise = [st for st in sched.get("stages", []) if st.get("type") == "minimise"]
    families = [_Family(i, target, int(st.get("count_each", 0)))
                for i, (st, target) in enumerate((st, t) for st in minimise for t in st.get("targets") or [])]
    options = dict(model.config.solve.solver_options or {})
    explore = sum(int(st.get("count", 0)) for st in sched.get("stages", []) if st.get("type") == "explore")
    k = max(1, min(int(options.get("spores_explore_chains", 1) or 1), explore or 1))
    chains, first = [], 1
    for c in range(k if k > 1 else 0):
        n = explore // k + (1 if c < explore % k else 0)
        chains.append(_Chain(c, n, first))
        first += n
    jobs = chains[1:] + families
    workers = options.get("spores_workers", "auto")
    workers = "auto" if str(workers).lower() == "auto" else max(1, int(workers or 1))
    ctx = _Ctx(model, sched)
    pool = None
    if jobs and workers != 1:
        if highs:
            part_dir = os.path.join(os.path.dirname(os.path.abspath(netcdf)), "spores_parts")
            pool = _Pool(ctx, jobs, workers, part_dir)
            highs_solver.HOOKS["on_loaded"] = pool.fork
            highs_solver.HOOKS["on_solved"] = pool.baseline
        else:
            if "spores_workers" in options:
                print("SPORES: spores_workers needs solver highs; running the minimise targets serially", flush=True)
    if chains:
        print(f"SPORES: {explore} explore iteration(s) as {len(chains)} independent chain(s) of "
              f"{', '.join(str(c.count) for c in chains)}", flush=True)

    model.build()
    native_spores = {"number": chains[0].count} if chains else None
    if not explore:
        # Calliope's SPORES loop refuses number 0 (it checks 0 >= number even
        # on a fresh run), so a minimise-only schedule runs it with 1 and skips
        # that explore solve: an empty result ends the loop after the baseline.
        solve = model.backend._solve

        def _skip_explore(solve_config, warmstart=False):
            if model.backend.objective == "min_spores":
                print("SPORES: no explore stage; skipping Calliope's explore iteration", flush=True)
                skipped = xr.Dataset()
                skipped.attrs["termination_condition"] = "skipped"
                return skipped
            return solve(solve_config, warmstart=warmstart)

        model.backend._solve = _skip_explore
        native_spores = {"number": 1}
    # native: baseline + explore iterations (chain 0 only, with chains)
    model.solve(**({"spores": native_spores} if native_spores else {}))
    native = model.results
    parts = [native.sel(spores=s, drop=True) for s in native["spores"].values]
    labels = [{"stage": "cost_optimal" if i == 0 else "explore", "target": None, "iteration": i}
              for i in range(len(parts))]
    if chains:
        for lab in labels[1:]:
            lab["chain"] = 0

    if pool is not None:
        more, more_labels = pool.collect()
        parts += more
        labels += more_labels
    elif jobs:
        score0 = ctx.scores(parts[0])
        total = sum(j.count for j in jobs)
        done = 0
        for job in jobs:
            for k, res in ctx.run(job, score0, lambda k, base=done: f"{base + k + 1}/{total}"):
                parts.append(res)
                labels.append(_label(job, k))
            done += job.count

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
