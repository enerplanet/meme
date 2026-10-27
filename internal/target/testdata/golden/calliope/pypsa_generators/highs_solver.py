#!/usr/bin/env python
"""HiGHS solver bridge for Calliope 0.7.0.dev7 (pyomo 6.8.x).

Pyomo's HiGHS plugins cannot drive Calliope's pyomo *kernel* model (the appsi
and contrib persistent interfaces reject or break on it), so `solver: highs`
bypasses pyomo's solver layer entirely. Every Calliope solve goes through
PyomoBackendModel._solve — the plain run, the native SPORES loop, operate
windows and the SPORES driver's minimise stages — so install() replaces that one
method. The first solve writes the built kernel model as an LP file (pyomo's own
writer, the same one the cbc plugin uses) and reads it into one highspy.Highs
that is kept for the whole run. Later solves (SPORES iterations) update that
instance in place: the objective is recomputed, and only the constraints and
variable bounds that Calliope rebuilt or whose parameters changed since the last
solve are refreshed, so the LP is not rewritten every iteration. A change the
in-place path cannot express (rebuilt variables, fixed/unfixed variables,
non-linear rows) falls back to rewriting the LP. The primal (and, when tracked,
dual) values are loaded back onto the pyomo objects before Calliope's normal
results/postprocessing path runs. Any other solver name falls through to
Calliope's original method.

Placeholder bounds: model data often encodes "unlimited" as a huge finite
number (e.g. a transmission link with 1e12 kW existing capacity), which wrecks
the IPM's scaling. A column fixed at |v| >= placeholder_bound (default 1e10) is
substituted out exactly (x = v + x', x' fixed at 0); any remaining row or column
bound with |b| >= placeholder_bound is relaxed to infinity. After the solve the
relaxed bounds are checked against the solution: if one would have been
violated, the original bounds are restored and the solve is repeated, so the
relaxation never changes the answer.

Run as a script, it replaces `calliope run` for a plain (non-schedule) run:

    python highs_solver.py <model.yaml> <results.nc> <csv_dir>

HiGHS options: defaults below, then config.solve.solver_options (HiGHS option
names, e.g. time_limit, threads, run_crossover, solver) override them; the
bridge's own options (keep_lp, placeholder_bound, placeholder_clamp,
spores_run_crossover, spores_cost_tiebreak, spores_gurobi, spores_gurobi_python)
and the SPORES driver's (spores_workers, spores_explore_chains) are taken out
before the rest reach HiGHS. An interior-point solve that stalls is retried
(see _solve_highs); a SPORES solve that no retry solves goes to Gurobi when it
is available (see _GRB_STATUS).
The HiGHS log is written to <output dir>/highs.log and echoed to stdout as it
runs.
"""
import collections
import os
import re
import resource
import shutil
import subprocess
import sys
import tempfile
import time
import types

import highspy
import numpy as np
import xarray as xr
from pyomo.core.kernel.piecewise_library.transforms import PiecewiseLinearFunction, piecewise_inc
from pyomo.repn.standard_repn import generate_standard_repn

import calliope
from calliope.backend import pyomo_backend_model as _pbm

# Bounds at or beyond this magnitude are placeholders for "unlimited", not
# constraints (see the module docstring). Override with the solver option
# placeholder_bound; 0 disables the substitution and relaxation.
PLACEHOLDER_BOUND = 1e10

# Constraint groups whose bounds are real values however large, never
# placeholders: the SPORES cost cap is spores_baseline_cost * (1 + slack),
# ~6e10 EUR on the Italy full year. Relaxing it dropped the cost cap from the
# first SPORES solve; restoring every relaxation then left IPX stalling.
_EXACT_ROWS = frozenset({"total_system_cost_max"})

# Defaults for Calliope's continuous planning LPs: IPX, HiGHS's interior-point
# solver, then crossover to a vertex. HiPO (solver "hipo", needs the
# highspy-extras package) iterates ~10x faster on the Italy full-year LP but
# stagnates there at a 2-30% gap (then HiGHS falls back to simplex from
# scratch, hours without progress), and it is no faster on the week model, so
# it is opt-in. Crossover keeps the cost-optimal results exact
# (zero capacities stay exactly zero, objectives match simplex to the cent).
# SPORES solves (any objective other than the build's cost objective) skip it
# by default (spores_run_crossover): they only need near-optimal capacities,
# and on a full-year model crossover can take hours. The LPs are degenerate,
# so per-node capacities are not unique even with crossover.
# Explore SPORES solves (objective min_spores) also add spores_cost_tiebreak x
# the total system cost (the SPORES cost cap's expression, in EUR) to their
# objective. min_spores costs only ~100 of millions of columns, so its optimal
# face is huge and IPX stalled on it (Italy 3H full year: always at
# user_bound_scale -16, by chance at -12). With 1e-6 it solved to the same
# score (6.7342e7, ~0.1 % added); 1e-5 broke it. Minimise solves
# (tempo_min_spores) converge without it and stalled with it (week model), so
# they are left alone. 0 turns the tie-break off.
# SPORES solves walk a ladder of user_bound_scale offsets from bound_scale():
# IPX converges or stops with "no progress" (model status Unknown) on them
# depending on the scale, and the scale that works differs from LP to LP. Seen
# on the Italy 3H full year (bound_scale() -16), replaying each LP bit for bit:
# - explore: the first explore LP stalled (~1 h) at -16 and -12 and converged
#   at -13/-14 (~2 h); of five explore LPs, four converged at -13 and one
#   stalled at -13, -14, -12, -11 and -15 but converged at -16 (2.5 h).
# - minimise: three of four converged at -16; one stopped at iteration 19
#   (~10 min) at -16 and converged at -12, -13 and -14 (1.7 h).
# - 178-SPORE run: of 21 explore LPs, 7 converged at -13, 5 at -16 and 8 at
#   -14; the 21st stalled or failed at -13, -16, -14, -12, -17 and -10 and
#   converged at -15, -18, -11 and -8 (~2 h each). Ten minimise LPs failed
#   -16, -13, -14 and -12 and went on to the fallback, which ran 30-65 h.
# On an explore LP the crossover/dual simplex fallback ran 39 h without
# finishing, so explore solves stop after the ladder. A minimise solve gets it
# last (it finished that minimise LP, in 39 h, in a worker).
_EXPLORE_SCALES = (3, 0, 2, 4, 1, -2, 5)
_MINIMISE_SCALES = (0, 3, 2, 4, 1, -2, 5)

# A SPORES LP that every bound scale fails goes to Gurobi's barrier with
# crossover when Gurobi is available (spores_gurobi; it runs in a subprocess,
# spores_gurobi_python, so it can live in its own venv). On the Italy 3H full
# year it solved the 21st explore LP of the 178-SPORE run in 18 min (HiGHS:
# 2 h at the scales that converge; same objective, score capacities equal to
# 1e-6) and a minimise LP that failed all seven scales in 2-4 min. Without
# crossover its barrier stopped short ("sub-optimal") on both. A minimise
# solve still gets HiGHS's crossover after it, should Gurobi fail.
_GRB_STATUS = {2: "kOptimal", 3: "kInfeasible", 4: "kUnboundedOrInfeasible", 5: "kUnbounded", 9: "kTimeLimit"}

DEFAULTS = {
    "solver": "ipx",
    "run_crossover": "on",
    "spores_run_crossover": "off",
    "spores_cost_tiebreak": 1e-6,
    "spores_gurobi": "last",
    "spores_gurobi_python": "",
    "threads": os.cpu_count() or 1,
}

# HiGHS model status -> pyomo TerminationCondition name (what Calliope reports
# in results.attrs["termination_condition"]).
_STATUS = {
    "kOptimal": "optimal",
    "kInfeasible": "infeasible",
    "kUnboundedOrInfeasible": "infeasibleOrUnbounded",
    "kUnbounded": "unbounded",
    "kTimeLimit": "maxTimeLimit",
    "kIterationLimit": "maxIterations",
    "kInterrupt": "userInterrupt",
}

_INF = highspy.kHighsInf
_LOG_FILE = None  # set by install(); one log for every solve of the run
_SOLVE_COUNT = 0
_LABEL = ""  # log prefix, e.g. "[w3] " in a SPORES driver worker process

# Solver options the bridge or the SPORES driver consume; never passed to HiGHS.
_OWN_OPTIONS = ("keep_lp", "placeholder_bound", "placeholder_clamp", "spores_cost_tiebreak", "spores_explore_chains",
                "spores_gurobi", "spores_gurobi_python", "spores_run_crossover", "spores_workers")

# Driver hooks: on_loaded(backend) after a full LP load and before it is
# solved; on_solved(backend, results) after each optimal solve.
HOOKS = {"on_loaded": None, "on_solved": None}

# Options forced over the user's (a driver worker caps its threads).
OVERRIDES = {}


class _Rebuild(Exception):
    """The pending model changes need a full LP rewrite."""


def _apply_options(h, options):
    for key, value in options.items():
        status = h.setOptionValue(key, value)
        if status != highspy.HighsStatus.kOk and isinstance(value, (int, float)) and not isinstance(value, bool):
            # e.g. time_limit given as an int, threads given as a float
            alt = float(value) if isinstance(value, int) else int(value)
            status = h.setOptionValue(key, alt)
        if status != highspy.HighsStatus.kOk:
            print(f"HiGHS: ignoring option {key}={value!r} (rejected by HiGHS)", flush=True)


def _scratch_dir():
    """Where the throwaway LP goes: tmpfs when it has room (the full-year LP
    is ~1 GiB; the container needs a large --shm-size), else the run dir."""
    try:
        if shutil.disk_usage("/dev/shm").free >= 8 * 2**30:
            return "/dev/shm"
    except OSError:
        pass
    return os.path.dirname(_LOG_FILE) if _LOG_FILE else None


def _memory():
    """Peak RSS, plus (in a forked SPORES worker) the memory it does not share
    with the parent, which is what sizes the worker pool."""
    text = f"peak RSS {resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 2**20:.1f} GiB"  # KiB on Linux
    if _LABEL:
        try:
            with open("/proc/self/smaps_rollup") as fh:
                kib = sum(int(line.split()[1]) for line in fh if line.startswith(("Private_Clean:", "Private_Dirty:")))
            text += f", private {kib / 2**20:.1f} GiB"
        except OSError:
            pass
    return text


def _bound(value, default):
    return default if value is None else float(value)


_COMPONENT_NAMES = {}


def _component(obj):
    """Calliope component name of a pyomo object, e.g. 'flow_cap' (cached per
    parent list: pyomo's getname walks the tree and is slow)."""
    if obj is None:
        return "?"
    parent = obj.parent
    key = id(parent)
    hit = _COMPONENT_NAMES.get(key)
    if hit is not None and hit[0] is parent:
        return hit[1]
    full = obj.getname(fully_qualified=True)
    m = re.match(r"\w+[\[(]'?(\w+)", full)
    name = m.group(1) if m else full
    _COMPONENT_NAMES[key] = (parent, name)
    return name


class _Model:
    """The highspy model of one Calliope backend, kept across its solves.

    Columns and rows are tied to pyomo objects by id (with strong references
    kept so ids stay unique). Rows of a refreshed constraint are retired in
    place (made free, which presolve drops) and its new rows appended, so
    indices never shift."""

    def __init__(self, backend, threshold, clamp=None):
        self.backend = backend
        self.requested = threshold  # placeholder_bound this model was built for
        self.threshold = threshold  # 0 once relaxations were found to bind
        self.clamp_req = clamp  # placeholder_clamp: None, "auto" or a number
        self.clamp = _INF  # what relaxed placeholder bounds are set to
        self.h = None
        self.col_var = []
        self.var_col = {}
        self.row_con = []
        self.con_rows = {}
        self.comp_cons = {}  # constraint component name -> its pyomo objects
        self.shift = np.zeros(0)  # col -> constant substituted out of it
        self.relaxed_cols = {}  # col -> original (lb, ub), after any shift
        self.relaxed_rows = {}  # row -> original (lb, ub)
        self.objective = None
        self.obj_cost = np.zeros(0)  # the objective's column costs, without a tie-break
        self.tiebreak = None  # ((eps, cost cap row ids), its columns) while in the objective
        self.cost_terms = None  # (cost cap row ids, columns, coefficients)
        self.is_mip = False
        self.max_bound = 0.0
        self.dead_rows = 0
        self.dirty_inputs = set()
        self.dirty_components = set()  # (component type, name)
        self.dirty_var_bounds = set()
        self.rebuild = None  # reason a full rewrite is pending

    # -- full load ---------------------------------------------------------

    def load(self, keep_lp, workdir):
        """Write the LP, read it into a fresh highspy.Highs, map it to pyomo."""
        t0 = time.time()
        if keep_lp:
            os.makedirs(workdir, exist_ok=True)
            lp_path = os.path.join(workdir, f"model_{_SOLVE_COUNT}.lp")
        else:
            fd, lp_path = tempfile.mkstemp(suffix=".lp", dir=_scratch_dir())
            os.close(fd)
        inst = self.backend._instance
        try:
            smap = inst.write(lp_path, format="lp", symbolic_solver_labels=keep_lp)
            size = os.path.getsize(lp_path) / 2**20
            t_write = time.time() - t0
            self.h = highspy.Highs()
            log = {"log_to_console": True, "output_flag": True}
            if _LOG_FILE:
                log["log_file"] = _LOG_FILE
            _apply_options(self.h, log)
            if self.h.readModel(lp_path) == highspy.HighsStatus.kError:
                raise RuntimeError(f"HiGHS could not read the LP written by pyomo ({lp_path})")
        finally:
            if not keep_lp and os.path.exists(lp_path):
                os.remove(lp_path)

        by_symbol, aliases = smap.bySymbol, smap.aliases
        lp = self.h.getLp()
        self.is_mip = any(int(v) != 0 for v in lp.integrality_)
        self.col_var = [by_symbol.get(name) for name in lp.col_names_]
        self.var_col = {id(v): j for j, v in enumerate(self.col_var) if v is not None}
        self.row_con = []
        self.con_rows = {}
        for i, name in enumerate(lp.row_names_):
            con = by_symbol.get(name)
            if con is None and name in aliases:
                con = aliases[name]
                con = con() if callable(con) and not hasattr(con, "body") else con
            self.row_con.append(con)
            if con is not None:
                self.con_rows.setdefault(id(con), []).append(i)
        self.comp_cons = {name: list(cons) for name, cons in inst.constraints.items()}
        self.shift = np.zeros(lp.num_col_)
        self.relaxed_cols, self.relaxed_rows = {}, {}
        self.objective = self._active_objective()
        self.obj_cost = np.asarray(lp.col_cost_, dtype=float).copy()
        self.tiebreak = None
        self.dead_rows = 0
        self._clear_dirty()
        t1 = time.time()
        self._substitute_and_relax(lp)
        return (f"LP written in {t_write:.1f}s ({size:.1f} MiB), read and mapped in {t1 - t0 - t_write:.1f}s, "
                f"placeholder bounds handled in {time.time() - t1:.1f}s")

    def _note_bounds(self, values):
        """Track the largest finite |bound| in the model (for user_bound_scale)."""
        v = np.abs(np.asarray(values, dtype=float))
        v = v[np.isfinite(v) & (v < _INF)]
        if v.size:
            self.max_bound = max(self.max_bound, float(v.max()))

    def bound_scale(self):
        """user_bound_scale that brings the largest bound to at most 1e6.

        Calliope bounds in kW/kWh/EUR reach 1e8-1e9 (demand, the SPORES cost
        row); at that size IPX's absolute residuals stall it ("no progress")
        short of crossover and HiGHS falls back to simplex from scratch, which
        took longer than the IPM itself. A power-of-two scale is exact."""
        if self.max_bound <= 1e6:
            return 0
        return -int(np.ceil(np.log2(self.max_bound / 1e6)))

    def _active_objective(self):
        return self.backend.objectives[self.backend.objective].item()

    def _clear_dirty(self):
        self.dirty_inputs.clear()
        self.dirty_components.clear()
        self.dirty_var_bounds.clear()
        self.rebuild = None

    def _substitute_and_relax(self, lp):
        """Substitute huge fixed columns out and relax huge bounds (whole model)."""
        t = self.threshold
        if not t:
            for arr in (lp.col_lower_, lp.col_upper_, lp.row_lower_, lp.row_upper_):
                self._note_bounds(arr)
            return
        h = self.h
        clo, cup = np.asarray(lp.col_lower_, dtype=float).copy(), np.asarray(lp.col_upper_, dtype=float).copy()
        rlo, rup = np.asarray(lp.row_lower_, dtype=float).copy(), np.asarray(lp.row_upper_, dtype=float).copy()
        a = lp.a_matrix_
        if a.format_ != highspy.MatrixFormat.kColwise:
            print("HiGHS: LP matrix not column-wise; placeholder bounds left as they are", flush=True)
            return
        start = np.asarray(a.start_)
        e_row, e_val = np.asarray(a.index_), np.asarray(a.value_)
        e_col = np.repeat(np.arange(lp.num_col_), np.diff(start))
        cost = np.asarray(lp.col_cost_)

        # Substitute fixed placeholder columns out. A row left with a single
        # column and equal bounds pins that column (e.g. Calliope's
        # flow_cap[node] == link_flow_cap of a transmission link), so it is
        # substituted in the next round.
        subst = collections.Counter()
        shifted = np.zeros(lp.num_col_, dtype=bool)
        touched = np.zeros(lp.num_row_, dtype=bool)
        offset = 0.0
        fixed = np.isfinite(clo) & (clo == cup) & (np.abs(clo) >= t)
        while fixed.any():
            idx = np.flatnonzero(fixed)
            vals = clo[idx]
            self.shift[idx] = vals
            shifted[idx] = True
            clo[idx] = cup[idx] = 0.0
            offset += float(cost[idx] @ vals)
            sel = fixed[e_col]
            row_shift = np.bincount(e_row[sel], weights=e_val[sel] * self.shift[e_col[sel]], minlength=lp.num_row_)
            hit = row_shift != 0
            rlo[hit] -= row_shift[hit]  # inf - x stays inf
            rup[hit] -= row_shift[hit]
            touched[e_row[sel]] = True
            for j in idx:
                subst[_component(self.col_var[j])] += 1
            # rows of this round now left with one free column and equal bounds
            live = ~shifted[e_col]
            nnz = np.bincount(e_row[live], minlength=lp.num_row_)
            fixed = np.zeros(lp.num_col_, dtype=bool)
            for k in np.flatnonzero(live & (nnz[e_row] == 1) & touched[e_row]):
                i, j = e_row[k], e_col[k]
                if rlo[i] == rup[i] and np.isfinite(rlo[i]) and abs(rlo[i] / e_val[k]) >= t:
                    v = rlo[i] / e_val[k]
                    if clo[j] <= v <= cup[j] and not fixed[j]:
                        clo[j] = cup[j] = v
                        fixed[j] = True
        if shifted.any():
            idx = np.flatnonzero(shifted).astype(np.int32)
            h.changeObjectiveOffset(lp.offset_ + offset)
            h.changeColsBounds(len(idx), idx, np.zeros(len(idx)), np.zeros(len(idx)))
        touched = np.flatnonzero(touched)

        big_c = (np.isfinite(clo) & (np.abs(clo) >= t)) | (np.isfinite(cup) & (np.abs(cup) >= t))
        big_r = (np.isfinite(rlo) & (np.abs(rlo) >= t)) | (np.isfinite(rup) & (np.abs(rup) >= t))
        for i in np.flatnonzero(big_r):
            if _component(self.row_con[i]) in _EXACT_ROWS:
                big_r[i] = False
        # Relax to +-clamp instead of infinity when asked: an unbounded
        # placeholder (e.g. a lossless, costless link) leaves an unbounded
        # optimal face, which interior-point methods cannot centre on.
        if self.clamp_req:
            legit = 0.0
            for arr in (clo, cup, rlo, rup):
                v = np.abs(arr[np.isfinite(arr)])
                v = v[v < t]
                legit = max(legit, float(v.max()) if v.size else 0.0)
            self.clamp = legit if self.clamp_req == "auto" else float(self.clamp_req)
        else:
            self.clamp = _INF
        relaxed = collections.Counter()
        for j in np.flatnonzero(big_c):
            self.relaxed_cols[int(j)] = (clo[j], cup[j])
            relaxed[_component(self.col_var[j])] += 1
        for i in np.flatnonzero(big_r):
            self.relaxed_rows[int(i)] = (rlo[i], rup[i])
            relaxed[_component(self.row_con[i])] += 1
        # write back: shifted rows, then relaxed rows and columns
        rows = np.union1d(touched, np.flatnonzero(big_r)).astype(np.int32)
        c = self.clamp
        if len(rows):
            lo = np.where(np.abs(rlo[rows]) >= t, -c, rlo[rows])
            up = np.where(np.abs(rup[rows]) >= t, c, rup[rows])
            h.changeRowsBounds(len(rows), rows, lo, up)
        cols = np.flatnonzero(big_c).astype(np.int32)
        if len(cols):
            lo = np.where(np.abs(clo[cols]) >= t, -c, clo[cols])
            up = np.where(np.abs(cup[cols]) >= t, c, cup[cols])
            h.changeColsBounds(len(cols), cols, lo, up)
        for arr in (clo, cup, rlo, rup):
            self._note_bounds(np.where(np.abs(arr) >= t, 0.0, arr))
        if subst:
            print(f"HiGHS: substituted {sum(subst.values())} placeholder fixed column(s) (|v| >= {t:g}): "
                  + ", ".join(f"{k} {n}" for k, n in subst.most_common()), flush=True)
        if relaxed:
            to = "infinity" if c >= _INF else f"+-{c:g}"
            print(f"HiGHS: relaxed {big_r.sum()} row and {big_c.sum()} column placeholder bound(s) "
                  f"(|b| >= {t:g}) to {to}: " + ", ".join(f"{k} {n}" for k, n in relaxed.most_common()), flush=True)

    # -- in-place update ---------------------------------------------------

    def update(self):
        """Apply the changes Calliope made since the last solve in place."""
        t0 = time.time()
        if self.rebuild:
            raise _Rebuild(self.rebuild)
        b = self.backend
        names = {name for _, name in self.dirty_components}
        for name in self.dirty_inputs:
            if name in b._dataset:
                names |= b._find_all_references(b._dataset[name].attrs.get("references", set()))
        for typ, name in self.dirty_components:
            if typ in ("variables", "piecewise_constraints"):
                raise _Rebuild(f"{typ} {name} rebuilt")
        kinds = {name: (b._dataset[name].attrs.get("obj_type") if name in b._dataset else None) for name in names}
        if any(k == "piecewise_constraints" for k in kinds.values()):
            raise _Rebuild("piecewise constraints changed")

        var_names = {n for n, k in kinds.items() if k == "variables"} | self.dirty_var_bounds
        con_names = sorted(n for n in names if n in self.comp_cons or kinds.get(n) == "constraints")
        objective = self._active_objective()
        refresh_obj = objective is not self.objective or any(kinds.get(n) == "objectives" for n in names)

        for name in var_names:
            self._refresh_var_bounds(name)
        added = retired = 0
        for name in con_names:
            r, a = self._refresh_constraints(name)
            retired += r
            added += a
        if refresh_obj:
            self._refresh_objective(objective)
        self._clear_dirty()
        if self.dead_rows > 0.25 * len(self.row_con):
            self.rebuild = "too many retired rows"
        parts = []
        if con_names:
            parts.append(f"{len(con_names)} constraint group(s), {retired} row(s) out, {added} in")
        if var_names:
            parts.append(f"bounds of {len(var_names)} variable group(s)")
        if refresh_obj:
            parts.append("objective")
        return f"model updated in place in {time.time() - t0:.1f}s ({'; '.join(parts) or 'no changes'})"

    def _col(self, var):
        """Column of a pyomo variable, appending one if the LP lacks it."""
        j = self.var_col.get(id(var))
        if j is not None:
            return j
        if var.is_integer() or var.is_binary():
            raise _Rebuild("new integer variable")
        lb, ub = _bound(var.lb, -_INF), _bound(var.ub, _INF)
        j = len(self.col_var)
        self.col_var.append(var)
        self.var_col[id(var)] = j
        self.shift = np.append(self.shift, 0.0)
        t = self.threshold
        if t and lb == ub and abs(lb) >= t:
            self.shift[j], lb, ub = lb, 0.0, 0.0  # in no existing row yet
        elif t and (abs(lb) >= t or abs(ub) >= t) and (np.isfinite(lb) or np.isfinite(ub)):
            self.relaxed_cols[j] = (lb, ub)
            lb = -self.clamp if abs(lb) >= t else lb
            ub = self.clamp if abs(ub) >= t else ub
        self._note_bounds((lb, ub))
        self.h.addCol(0.0, lb, ub, 0, np.zeros(0, dtype=np.int32), np.zeros(0))
        return j

    def _refresh_var_bounds(self, name):
        t = self.threshold
        for var in self.backend._instance.variables.get(name, ()):
            j = self.var_col.get(id(var))
            if j is None:
                continue  # not in the LP yet; _col reads its bounds when it is
            if var.fixed:
                raise _Rebuild(f"variable {name} fixed")
            lb, ub = _bound(var.lb, -_INF), _bound(var.ub, _INF)
            if self.shift[j]:
                if lb == ub == self.shift[j]:
                    continue
                raise _Rebuild(f"bounds of substituted variable {name} changed")
            if t and lb == ub and abs(lb) >= t:
                raise _Rebuild(f"variable {name} fixed at a placeholder value")
            self.relaxed_cols.pop(j, None)
            if t and (abs(lb) >= t or abs(ub) >= t) and (np.isfinite(lb) or np.isfinite(ub)):
                self.relaxed_cols[j] = (lb, ub)
                lb = -self.clamp if abs(lb) >= t else lb
                ub = self.clamp if abs(ub) >= t else ub
            self._note_bounds((lb, ub))
            self.h.changeColBounds(j, lb, ub)

    def _refresh_constraints(self, name):
        """Retire the rows of constraint group `name`, append its current ones."""
        retired = []
        for con in self.comp_cons.pop(name, ()):
            for i in self.con_rows.pop(id(con), ()):
                retired.append(i)
                self.row_con[i] = None
                self.relaxed_rows.pop(i, None)
        if retired:
            idx = np.asarray(retired, dtype=np.int32)
            self.h.changeRowsBounds(len(idx), idx, np.full(len(idx), -_INF), np.full(len(idx), _INF))
            self.dead_rows += len(idx)

        cons = list(self.backend._instance.constraints.get(name, ()))
        self.comp_cons[name] = cons
        t = 0 if name in _EXACT_ROWS else self.threshold
        lower, upper, starts, index, value = [], [], [], [], []
        for con in cons:
            if not con.active:
                continue
            repn = generate_standard_repn(con.body, compute_values=True, quadratic=False)
            if not repn.is_linear():
                raise _Rebuild(f"non-linear constraint in {name}")
            if not repn.linear_vars:
                continue  # constant row: pyomo's LP writer drops it too
            cols = [self._col(v) for v in repn.linear_vars]
            offset = repn.constant + float(np.dot(self.shift[cols], repn.linear_coefs))
            lb, ub = _bound(con.lb, -_INF) - offset, _bound(con.ub, _INF) - offset
            i = len(self.row_con)
            self.row_con.append(con)
            self.con_rows.setdefault(id(con), []).append(i)
            if t and ((np.isfinite(lb) and abs(lb) >= t) or (np.isfinite(ub) and abs(ub) >= t)):
                self.relaxed_rows[i] = (lb, ub)
                lb = -self.clamp if abs(lb) >= t else lb
                ub = self.clamp if abs(ub) >= t else ub
            lower.append(lb)
            upper.append(ub)
            starts.append(len(index))
            index.extend(cols)
            value.extend(repn.linear_coefs)
        if lower:
            self._note_bounds(lower)
            self._note_bounds(upper)
            self.h.addRows(len(lower), np.asarray(lower), np.asarray(upper), len(index),
                           np.asarray(starts, dtype=np.int32), np.asarray(index, dtype=np.int32),
                           np.asarray(value, dtype=float))
        return len(retired), len(lower)

    def _refresh_objective(self, objective):
        repn = generate_standard_repn(objective.expr, compute_values=True, quadratic=False)
        if not repn.is_linear():
            raise _Rebuild("non-linear objective")
        cols = [self._col(v) for v in repn.linear_vars]
        costs = np.zeros(len(self.col_var))
        np.add.at(costs, np.asarray(cols, dtype=np.int64), np.asarray(repn.linear_coefs, dtype=float))
        n = len(costs)
        self.h.changeColsCost(n, np.arange(n, dtype=np.int32), costs)
        self.h.changeObjectiveOffset(repn.constant + float(costs @ self.shift))
        sense = highspy.ObjSense.kMaximize if objective.sense == -1 else highspy.ObjSense.kMinimize
        self.h.changeObjectiveSense(sense)
        self.objective = objective
        self.obj_cost = costs
        self.tiebreak = None

    def set_tiebreak(self, eps):
        """Objective = its own costs + eps x the SPORES cost cap's expression
        (minimised: a maximised objective subtracts it); eps 0 removes it.
        Returns the number of columns it touches (0 without a cost cap)."""
        cons = [c for c in self.comp_cons.get("total_system_cost_max", ()) if c.active] if eps else []
        key = (eps, tuple(id(c) for c in cons)) if cons else None
        if key == (self.tiebreak and self.tiebreak[0]):
            return len(self.tiebreak[1]) if key else 0
        costs = np.zeros(len(self.col_var))
        costs[:len(self.obj_cost)] = self.obj_cost
        self.tiebreak = None
        if key:
            if self.cost_terms is None or self.cost_terms[0] != key[1]:
                # the cost row is ~5e5 terms on a full year: expand it once, not
                # every SPORES iteration (each one refreshes the objective)
                cols, coefs = [], []
                for con in cons:
                    repn = generate_standard_repn(con.body, compute_values=True, quadratic=False)
                    cols.extend(self._col(v) for v in repn.linear_vars)
                    coefs.extend(repn.linear_coefs)
                self.cost_terms = (key[1], np.asarray(cols, dtype=np.int64), np.asarray(coefs, dtype=float))
                costs = np.append(costs, np.zeros(len(self.col_var) - len(costs)))  # _col may add columns
            _, cols, coefs = self.cost_terms
            sign = -1.0 if self.objective.sense == -1 else 1.0
            np.add.at(costs, cols, sign * eps * coefs)
            self.tiebreak = (key, np.unique(cols))
        n = len(costs)
        self.h.changeColsCost(n, np.arange(n, dtype=np.int32), costs)
        return len(self.tiebreak[1]) if key else 0

    # -- after the solve ---------------------------------------------------

    def violated_relaxations(self, sol):
        """Relaxed bounds the solution would have violated."""
        bad = []
        t, c = self.threshold, self.clamp
        for kind, relaxed, values in (("col", self.relaxed_cols, sol.col_value),
                                      ("row", self.relaxed_rows, sol.row_value)):
            for k, (lb, ub) in relaxed.items():
                # the bounds the solve had: a placeholder side is +-clamp
                lo = -c if abs(lb) >= t else lb
                up = c if abs(ub) >= t else ub
                x = values[k]
                if x < lb - 1e-6 * max(1.0, abs(lb)) or x > ub + 1e-6 * max(1.0, abs(ub)):
                    bad.append((kind, k, lb, ub, x))
                elif c < _INF and ((lo == -c and x <= -c * (1 - 1e-6)) or (up == c and x >= c * (1 - 1e-6))):
                    bad.append((kind, k, lo, up, x))  # sits at a clamp
        return bad

    def restore_relaxations(self):
        h = self.h
        for j, (lb, ub) in self.relaxed_cols.items():
            h.changeColBounds(j, lb, ub)
        for i, (lb, ub) in self.relaxed_rows.items():
            h.changeRowBounds(i, lb, ub)
        for relaxed in (self.relaxed_cols, self.relaxed_rows):
            for bounds in relaxed.values():
                self._note_bounds(bounds)
        self.relaxed_cols, self.relaxed_rows = {}, {}
        self.threshold = 0  # keep later additions exact too
        self.clamp = _INF

    def load_solution(self, sol):
        x = np.asarray(sol.col_value) + self.shift
        for var, value in zip(self.col_var, x):
            if var is not None:  # ONE_VAR_CONSTANT and friends have no pyomo var
                var.value = float(value)

    def load_duals(self, sol):
        # A ranged pyomo constraint is written as two rows (r_l_/r_u_); at most
        # one is active, so their duals add up.
        duals = {}
        for con, dual in zip(self.row_con, sol.row_dual):
            if con is not None:
                duals[con] = duals.get(con, 0.0) + dual
        dual_suffix = self.backend._instance.dual
        for con, dual in duals.items():
            dual_suffix[con] = dual


def _summary(info, solver):
    ipm = "hipo" if solver == "hipo" else "ipm"
    parts = [f"{ipm} {info.ipm_iteration_count} it" if info.ipm_iteration_count > 0 else f"no {ipm}"]
    if info.crossover_iteration_count > 0:
        parts.append(f"crossover {info.crossover_iteration_count} it")
    else:
        parts.append("no crossover")
    if info.simplex_iteration_count > 0:
        parts.append(f"simplex {info.simplex_iteration_count} it")
    return ", ".join(parts)


def _keep_lp(h, opts):
    """keep_lp: write the LP exactly as HiGHS is about to solve it (after the
    placeholder handling) plus the options, for benchmarking HiGHS directly:
    highs --options_file <name>.opt <name>.mps (or highspy readModel).

    The MPS file is not the same LP: it rounds ~1 in 10 numbers in the last
    digit and its reader drops free rows (the retired and relaxed ones). That
    changes IPX's path: on the Italy 3H explore LP, settings that converged on
    the MPS file stalled in the job. So <name>.npz keeps the arrays bit for
    bit; replay with _load_npz (same iterations and objective as the job)."""
    name = f"{os.path.splitext(_LOG_FILE)[0]}_solve{_SOLVE_COUNT}"  # highs_w3_solve2 in a SPORES worker
    t = time.time()
    h.writeModel(name + ".mps")
    _save_npz(h, name + ".npz")
    with open(name + ".opt", "w", encoding="utf-8") as fh:
        for key, value in opts.items():
            fh.write(f"{key} = {value}\n")
    print(f"HiGHS: {_LABEL}solve {_SOLVE_COUNT}: kept the LP as {name}.mps "
          f"({os.path.getsize(name + '.mps') / 2**20:.1f} MiB) and bit for bit as {name}.npz "
          f"({os.path.getsize(name + '.npz') / 2**20:.1f} MiB) in {time.time() - t:.1f}s, with its options in {name}.opt",
          flush=True)


def _load_npz(h, path):
    """Load an LP kept by keep_lp (<name>.npz) into highspy.Highs `h`, exactly."""
    d = np.load(path)
    lp = highspy.HighsLp()
    lp.num_col_, lp.num_row_ = len(d["col_cost"]), len(d["row_lower"])
    lp.col_cost_, lp.col_lower_, lp.col_upper_ = d["col_cost"], d["col_lower"], d["col_upper"]
    lp.row_lower_, lp.row_upper_ = d["row_lower"], d["row_upper"]
    lp.a_matrix_.format_ = highspy.MatrixFormat.kColwise
    lp.a_matrix_.num_col_, lp.a_matrix_.num_row_ = lp.num_col_, lp.num_row_
    lp.a_matrix_.start_, lp.a_matrix_.index_, lp.a_matrix_.value_ = d["start"], d["index"], d["value"]
    lp.offset_ = float(d["offset"])
    lp.sense_ = highspy.ObjSense.kMaximize if int(d["sense"]) else highspy.ObjSense.kMinimize
    if d["integrality"].any():
        lp.integrality_ = [highspy.HighsVarType(int(v)) for v in d["integrality"]]
    return h.passModel(lp)


def _save_npz(h, path):
    """The LP in `h` as arrays, bit for bit (see _keep_lp; _load_npz reads it)."""
    lp = h.getLp()
    a = lp.a_matrix_
    np.savez(path, col_cost=np.asarray(lp.col_cost_), col_lower=np.asarray(lp.col_lower_),
             col_upper=np.asarray(lp.col_upper_), row_lower=np.asarray(lp.row_lower_),
             row_upper=np.asarray(lp.row_upper_), start=np.asarray(a.start_), index=np.asarray(a.index_),
             value=np.asarray(a.value_), offset=lp.offset_, sense=int(lp.sense_ == highspy.ObjSense.kMaximize),
             integrality=np.asarray(lp.integrality_, dtype=np.int8))


# Run by spores_gurobi_python (needs gurobipy, numpy and scipy):
#   <lp.npz> <out.npz> <threads> <time limit s, 0 none> <log file> <duals 0/1>
# Rows lo == up are equalities; a ranged row becomes a <= and a >= row, whose
# duals add up again.
_GUROBI_SCRIPT = r"""
import sys
import numpy as np, scipy.sparse as sp, gurobipy as gp
inp, out, threads, limit, log, duals = sys.argv[1:7]
d = np.load(inp)
n, m = len(d["col_cost"]), len(d["row_lower"])
A = sp.csc_matrix((d["value"], d["index"], d["start"]), shape=(m, n)).tocsr()
inf = lambda a: np.clip(a, -gp.GRB.INFINITY, gp.GRB.INFINITY)
rl, ru = inf(d["row_lower"]), inf(d["row_upper"])
env = gp.Env(params={"LogFile": log, "LogToConsole": 0})
mdl = gp.Model(env=env)
x = mdl.addMVar(n, lb=inf(d["col_lower"]), ub=inf(d["col_upper"]), obj=d["col_cost"])
mdl.ModelSense = gp.GRB.MAXIMIZE if int(d["sense"]) else gp.GRB.MINIMIZE
mdl.ObjCon = float(d["offset"])
eq = rl == ru
groups = [(np.flatnonzero(eq), "=", rl), (np.flatnonzero(~eq & (ru < gp.GRB.INFINITY)), "<", ru),
          (np.flatnonzero(~eq & (rl > -gp.GRB.INFINITY)), ">", rl)]
cons = [(idx, mdl.addMConstr(A[idx], x, s, rhs[idx])) for idx, s, rhs in groups if len(idx)]
mdl.Params.Method, mdl.Params.Threads = 2, int(threads)
if float(limit) > 0:
    mdl.Params.TimeLimit = float(limit)
mdl.optimize()
res = {"status": mdl.Status, "runtime": mdl.Runtime, "bar_iter": mdl.BarIterCount, "iter": mdl.IterCount}
if mdl.SolCount:
    res["x"] = x.X
    res["row"] = A @ res["x"]
    res["obj"] = mdl.ObjVal
    if int(duals) and mdl.Status == gp.GRB.OPTIMAL:
        res["dual"] = np.zeros(m)
        for idx, c in cons:
            np.add.at(res["dual"], idx, np.asarray(c.Pi))
np.savez(out, **res)
"""

_GUROBI_OK = {}  # python -> whether it imports gurobipy (checked once per process)


def _gurobi_python(opts):
    """The interpreter that runs Gurobi for SPORES solves, or None."""
    if str(opts.get("spores_gurobi", "")).lower() in ("", "0", "off", "false", "no"):
        return None
    python = str(opts.get("spores_gurobi_python") or "") or sys.executable
    if python not in _GUROBI_OK:
        try:
            _GUROBI_OK[python] = subprocess.run([python, "-c", "import gurobipy, numpy, scipy"],
                                                capture_output=True, timeout=120).returncode == 0
        except (OSError, subprocess.SubprocessError):
            _GUROBI_OK[python] = False
        if not _GUROBI_OK[python] and opts.get("spores_gurobi_python"):
            print(f"HiGHS: {_LABEL}spores_gurobi_python {python} cannot import gurobipy, numpy and scipy; "
                  "no Gurobi rung", flush=True)
    return python if _GUROBI_OK[python] else None


def _solve_gurobi(h, python, opts, duals):
    """Solve the LP in `h` with Gurobi (barrier, then crossover) in `python`.
    Returns a HiGHS-like status name, a solution (col_value, row_value,
    row_dual and validity flags, in HiGHS's columns and rows), the objective
    and a summary for the log."""
    work = tempfile.mkdtemp(prefix="meme-gurobi-", dir=_scratch_dir())
    log = f"{os.path.splitext(_LOG_FILE)[0]}_solve{_SOLVE_COUNT}_gurobi.log" if _LOG_FILE else ""
    limit = float(opts.get("time_limit") or 0)
    try:
        inp, out = os.path.join(work, "lp.npz"), os.path.join(work, "sol.npz")
        _save_npz(h, inp)
        rc = subprocess.run([python, "-c", _GUROBI_SCRIPT, inp, out, str(int(opts.get("threads") or 1)),
                             str(limit if limit < _INF else 0), log, "1" if duals else "0"]).returncode
        if rc != 0 or not os.path.exists(out):
            return "kSolveError", None, None, f"Gurobi exited with code {rc}"
        with np.load(out) as r:
            res = {k: r[k] for k in r.files}
    finally:
        shutil.rmtree(work, ignore_errors=True)
    status = _GRB_STATUS.get(int(res["status"]), "kUnknown")
    summary = (f"Gurobi status {int(res['status'])}, barrier {int(res['bar_iter'])} it, "
               f"{int(res['iter'])} simplex it, {float(res['runtime']):.1f}s" + (f", log {log}" if log else ""))
    if "x" not in res:
        return status, None, None, summary
    sol = types.SimpleNamespace(col_value=res["x"], row_value=res["row"], value_valid=True,
                                row_dual=res.get("dual"), dual_valid="dual" in res)
    return status, sol, float(res["obj"]), summary


def _solve_highs(self, solve_config, warmstart=False):
    """Drop-in for PyomoBackendModel._solve with solve_config.solver == "highs"."""
    global _SOLVE_COUNT
    _SOLVE_COUNT += 1
    t0 = time.time()

    opts = dict(DEFAULTS)
    opts.update(dict(solve_config.solver_options or {}))
    opts.update(OVERRIDES)
    threshold = float(opts.pop("placeholder_bound", PLACEHOLDER_BOUND) or 0)
    clamp = opts.get("placeholder_clamp") or None
    # The first solve's objective is the cost objective (plain run, SPORES
    # baseline); set_objective rewrites config.objective, so remember it.
    if getattr(self, "_meme_base_objective", None) is None:
        self._meme_base_objective = self.objective
    spores = self.objective != self._meme_base_objective
    if spores:
        opts["run_crossover"] = opts["spores_run_crossover"]
    explore = self.objective == "min_spores"  # an explore SPORES solve
    tiebreak = float(opts.get("spores_cost_tiebreak") or 0) if explore else 0.0
    dump = str(opts.get("keep_lp", "")).lower() in ("1", "true", "on", "yes")
    gurobi = _gurobi_python(opts) if spores else None
    gurobi_first = str(opts.get("spores_gurobi", "")).lower() == "first"
    for key in _OWN_OPTIONS:
        opts.pop(key, None)
    keep = solve_config.save_logs is not None

    model = getattr(self, "_meme_highs", None)
    how = None
    if model is not None and model.requested == threshold and model.clamp_req == clamp:
        try:
            how = model.update()
        except _Rebuild as reason:
            print(f"HiGHS: {_LABEL}solve {_SOLVE_COUNT}: rewriting the LP ({reason})", flush=True)
    if how is None:
        model = _Model(self, threshold, clamp)
        self._meme_highs = model
        how = model.load(keep, str(solve_config.save_logs) if keep else None)
        if HOOKS["on_loaded"]:
            HOOKS["on_loaded"](self)
    print(f"HiGHS: {_LABEL}solve {_SOLVE_COUNT}: {how}", flush=True)
    model.dump = dump and _LOG_FILE is not None  # every solve, also one updated in place
    h = model.h
    if not model.is_mip and model.set_tiebreak(tiebreak):
        print(f"HiGHS: {_LABEL}solve {_SOLVE_COUNT}: objective + {tiebreak:g} x total system cost "
              "(spores_cost_tiebreak)", flush=True)

    user = dict(solve_config.solver_options or {})
    if model.is_mip:
        # MILP math: branch-and-bound picks its own LP solver.
        for key in ("solver", "run_crossover"):
            if key not in user:
                opts.pop(key)
    if opts.get("solver") == "hipo" and h.setOptionValue("solver", "hipo") != highspy.HighsStatus.kOk:
        print(f"HiGHS: {_LABEL}solve {_SOLVE_COUNT}: HiPO is not available in this highspy "
              "(pip install highspy-extras); using ipx", flush=True)
        opts["solver"] = "ipx"

    ipm = not model.is_mip and opts.get("solver") in ("hipo", "ipm", "ipx")
    ladder = [0]
    if ipm and spores and "user_bound_scale" not in user:
        ladder = list(_EXPLORE_SCALES if explore else _MINIMISE_SCALES)
    shift = [ladder[0]]  # user_bound_scale - bound_scale() of the current attempt
    grb = {}  # the last attempt's Gurobi result, when Gurobi made it
    duals = self.shadow_prices.is_active

    def run(retry=None):
        if "user_bound_scale" not in user:
            opts["user_bound_scale"] = model.bound_scale() + shift[0]
        grb.clear()
        if retry and retry.get("gurobi"):
            grb["status"], sol, grb["objective"], grb["summary"] = _solve_gurobi(h, gurobi, opts, duals)
            return sol
        opts.update(retry or {})
        if getattr(model, "dump", False):
            _keep_lp(h, opts)
            model.dump = False
        _apply_options(h, opts)
        h.run()
        return h.getSolution()

    def status_name():
        return grb["status"] if grb else h.getModelStatus().name

    # The interior-point solvers can stall ("no progress", model status
    # Unknown) on a SPORES LP. Seen on an Italy minimise solve with HiPO and no
    # crossover: deterministic, independent of threads, but gone with the bounds
    # scaled by a different power of two, and IPX solved it too.
    retries = []
    if len(ladder) > 1:
        # SPORES solves: next bound scale (see _EXPLORE_SCALES), then Gurobi
        # (see _GRB_STATUS) if available
        retries = [{"shift": s} for s in ladder[1:]]
        if gurobi and gurobi_first:
            retries.insert(0, {"shift": ladder[0]})
        elif gurobi:
            retries.append({"gurobi": True})
        if not explore:
            retries.append({"shift": 0, "solver": "ipx", "run_crossover": "on"})
    elif ipm:
        if opts["solver"] != "ipx" and "user_bound_scale" not in user:
            retries.append({"user_bound_scale": model.bound_scale() + 2})
        retries.append({"solver": "ipx", "run_crossover": "on", "user_bound_scale": model.bound_scale()})
    last = {"gurobi": True} if gurobi and gurobi_first and len(ladder) > 1 else None
    if last:
        print(f"HiGHS: {_LABEL}solve {_SOLVE_COUNT}: solving with Gurobi first (spores_gurobi first)", flush=True)
    sol = run(last)
    while status_name() not in _STATUS and retries:
        retry = retries.pop(0)
        if "shift" in retry:
            shift[0] = retry.pop("shift")
            retry["user_bound_scale"] = model.bound_scale() + shift[0]
        failed = (f"{grb['status'][1:]} with Gurobi ({grb['summary']})" if grb else
                  f"{h.modelStatusToString(h.getModelStatus())} with {opts['solver']}")
        print(f"HiGHS: {_LABEL}solve {_SOLVE_COUNT}: {failed} after {time.time() - t0:.1f}s; solving again with "
              + ("Gurobi barrier and crossover" if retry.get("gurobi") else
                 ", ".join(f"{k} {v}" for k, v in retry.items())), flush=True)
        if "user_bound_scale" in user:
            retry.pop("user_bound_scale")
        last = dict(retry)
        sol = run(retry)
    bad = model.violated_relaxations(sol) if status_name() == "kOptimal" else []
    if bad:
        kind, k, lb, ub, x = bad[0]
        print(f"HiGHS: {_LABEL}solve {_SOLVE_COUNT}: {len(bad)} relaxed placeholder bound(s) would bind "
              f"(e.g. {kind} {k}: {x:g} outside [{lb:g}, {ub:g}]); restoring them and solving again", flush=True)
        model.restore_relaxations()
        sol = run(last if grb else None)

    termination = _STATUS.get(status_name(), "other")
    has_primal = sol is not None and sol.value_valid
    if grb:
        objective, summary = grb["objective"], grb["summary"]
    else:
        info = h.getInfo()
        objective, summary = info.objective_function_value, _summary(info, opts.get("solver"))
    if termination == "optimal" and not grb and info.primal_solution_status != 2:  # kSolutionStatusFeasible
        # HiGHS solved the user-scaled model to optimality, but unscaled a row
        # misses the absolute 1e-7 tolerance (seen: 7e-6 on a 1e9 cost row,
        # 7e-15 relative). The solution is optimal for all practical purposes.
        print(f"HiGHS: {_LABEL}solve {_SOLVE_COUNT}: optimal, but the unscaled solution exceeds the absolute feasibility "
              f"tolerance (max primal infeasibility {info.max_primal_infeasibility:g}); accepted", flush=True)

    if termination == "optimal" and has_primal:
        model.load_solution(sol)
        if self.shadow_prices.is_active and sol.dual_valid:
            model.load_duals(sol)
        results = self.load_results()
        scale = "" if grb else f", user_bound_scale {opts.get('user_bound_scale', 0)}"
        print(f"HiGHS: {_LABEL}solve {_SOLVE_COUNT}: optimal, objective {objective:.10g}, "
              f"{time.time() - t0:.1f}s total ({summary}{scale}, {_memory()})",
              flush=True)
        if HOOKS["on_solved"]:
            HOOKS["on_solved"](self, results)
    else:
        what = f"{grb['status'][1:]} with Gurobi ({grb['summary']})" if grb else h.modelStatusToString(h.getModelStatus())
        self._solve_logger.critical(f"HiGHS model status: {what}")
        print(f"HiGHS: {_LABEL}solve {_SOLVE_COUNT}: {what} after {time.time() - t0:.1f}s", flush=True)
        _pbm.model_warn("Model solution was non-optimal.", _class=_pbm.BackendWarning)
        results = xr.Dataset()
        if termination == "optimal":  # optimal status but no primal values to load
            termination = "other"

    results.attrs["termination_condition"] = termination
    return results


class _ObjPiecewiseInc(piecewise_inc, _pbm.CoordObj):
    """Calliope's piecewise constraint wrapper on pyomo's incremental (binary)
    formulation instead of SOS2, which HiGHS cannot read. Same feasible set,
    so the same optimum."""

    def __init__(self, **kwds):
        func = PiecewiseLinearFunction(breakpoints=kwds.pop("breakpoints"), values=kwds.pop("values"))
        piecewise_inc.__init__(self, func, **kwds)
        _pbm.CoordObj.__init__(self)

    def getname(self, *args, **kwargs):
        return self._update_name(piecewise_inc.getname(self, *args, **kwargs))


def _tracking(orig, record):
    """Wrap a backend method so the HiGHS model of that backend (if any) hears
    about the change it makes."""
    def wrapper(self, *args, **kwargs):
        out = orig(self, *args, **kwargs)
        model = getattr(self, "_meme_highs", None)
        if model is not None:
            record(model, *args, **kwargs)
        return out
    wrapper._meme_highs = True
    return wrapper


def _rebuild_on(reason):
    def record(model, *args, **kwargs):
        model.rebuild = model.rebuild or reason
    return record


def install(log_dir=None):
    """Route `solver: highs` solves through highspy; other solvers are
    untouched. log_dir receives highs.log (one file across all solves).

    Call it before model.build(), and only for a highs run: it also makes
    piecewise constraints build without SOS2 (see _ObjPiecewiseInc)."""
    global _LOG_FILE
    if log_dir:
        os.makedirs(log_dir, exist_ok=True)
        _LOG_FILE = os.path.join(log_dir, "highs.log")
    _pbm.ObjPiecewiseConstraint = _ObjPiecewiseInc
    cls = _pbm.PyomoBackendModel
    orig = cls._solve
    if getattr(orig, "_meme_highs", False):
        return

    def _solve(self, solve_config, warmstart=False):
        if solve_config.solver == "highs":
            return _solve_highs(self, solve_config, warmstart)
        return orig(self, solve_config, warmstart=warmstart)

    _solve._meme_highs = True
    cls._solve = _solve

    # What changed since the last solve, for the in-place update.
    cls.update_input = _tracking(cls.update_input, lambda m, name, *a, **k: m.dirty_inputs.add(name))
    cls.delete_component = _tracking(
        cls.delete_component, lambda m, key, component_type, *a, **k: m.dirty_components.add((component_type, key)))
    cls._add_to_dataset = _tracking(
        cls._add_to_dataset, lambda m, name, da, obj_type, *a, **k: m.dirty_components.add((obj_type, name)))
    cls.update_variable_bounds = _tracking(
        cls.update_variable_bounds, lambda m, name, *a, **k: m.dirty_var_bounds.add(name))
    cls.fix_variable = _tracking(cls.fix_variable, _rebuild_on("variables fixed"))
    cls.unfix_variable = _tracking(cls.unfix_variable, _rebuild_on("variables unfixed"))


def main(entry, netcdf, csv_dir):
    """`calliope run <entry> --save_netcdf <netcdf> --save_csv <csv_dir>` with
    the HiGHS bridge installed."""
    start = time.time()
    calliope.set_log_verbosity("info", include_solver_output=True)
    install(os.path.dirname(os.path.abspath(netcdf)))
    model = calliope.read_yaml(entry)
    model.build()
    model.solve()
    print(f"Saving CSV results to directory: {csv_dir}", flush=True)
    model.to_csv(csv_dir)
    print(f"Saving NetCDF results to file: {netcdf}", flush=True)
    model.to_netcdf(netcdf)
    termination = model.runtime.termination_condition
    print(f"Calliope run complete ({termination}). Elapsed: {time.time() - start:.1f} seconds", flush=True)
    if termination != "optimal":  # as `calliope run` (fail_when_infeasible)
        sys.exit(1)


if __name__ == "__main__":
    if len(sys.argv) != 4:
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    main(*sys.argv[1:])
