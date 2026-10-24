# Contributing to MEME

Thank you for taking the time to contribute. MEME is a Go service that
translates one canonical energy-system model into the native inputs of PyPSA,
Calliope and AdOpT-NET0 and runs them; most contributions touch an emitter, a
validation gate, the scenario corpus or the documentation. This guide covers
the workflow and the project's conventions. By participating you agree to the
[Code of Conduct](https://github.com/enerplanet/meme/blob/main/CODE_OF_CONDUCT.md).

## Ways to contribute

- Report a translation defect: a payload that validates but produces a
  different problem on one framework than on another. Include the payload
  and what each target emitted or solved; the issue tracker is at
  <https://github.com/enerplanet/meme/issues>.
- Add a capability: a canonical field a second framework can honor.
- Add or update a framework release (see "Framework versions" below).
- Improve the documentation at <https://enerplanet.github.io/meme>.

## Development setup

- Host work needs Go (the version in `go.mod`) and `make`; no Python. The
  default runner is a dry-runner, so the API validates and translates
  without any framework installed.
- Real solvers run in the container: `make -C environment build ENV=dev`
  once, then `make e2e` (see the
  [environment guide](https://enerplanet.github.io/meme/operations/environment/)).
- The payload-contract check needs `python3` with `jsonschema >= 4.18`.

```bash
make test           # vet + unit tests + golden files + validation matrix
make test-race      # race detector over the concurrent packages
make schema-check   # examples and corpus against schemas/revised_unified_schema.json
make e2e-smoke      # container: one real-solver lifecycle per target, one multi-target job, the PyPSA feature checks (~30 s)
make e2e            # container: the whole corpus on real solvers (~2 min)
make docs           # build the documentation site strictly
```

## Workflow

1. Branch from `main` with a typed prefix — `feat/`, `fix/`, `docs/`,
   `test/`, `chore/`, `refactor/` — and a short kebab-case description
   (`fix/calliope-operate-limits`). `main` is protected: changes land
   through a pull request with a passing CI run.
2. Commit in [Conventional Commits](https://www.conventionalcommits.org)
   form: `type(scope): subject`, lowercase subject, no trailing period,
   at most 100 characters (`fix(calliope): write node-indexed inputs for
   every node a tech is placed at`). A body explains why; `Closes #N`
   links the issue. The commit-lint workflow enforces the format.
3. Keep each commit buildable and green: the golden and matrix changes a
   commit causes belong in that commit.
4. Open the pull request against `main` with what changed, why, how it was
   verified, and the related issue.
5. Land it with the signatures intact: fast-forward `main` to the reviewed
   branch head from your machine (`git push origin <branch>:main`), or use
   GitHub's *Squash and merge* or *Create a merge commit*, which GitHub signs.
   *Rebase and merge* recreates the commits on GitHub's side and drops their
   signatures, so `main` ends up with unverified commits.

## What a change must carry

- **Tests first, where the change fixes a defect.** Write the test that
  fails on `main`, then the fix. For emitter work the unit test asserts on
  the emitted file; for a semantic fix, add the payload to the scenario
  corpus so the real-solver suite pins it.
- **Nothing silently dropped.** A property the selected target cannot honor
  is rejected in `ValidateJob` with an error naming the property and the
  target, or named in a warning. Validation verdicts are frozen in
  `internal/target/testdata/golden/validation_matrix.json`.
- **Goldens reviewed, not just regenerated.** `make golden-update` accepts an
  intended emitter change; read the diff — it *is* the contract change —
  and check that renames are byte-identical and content changes are the
  intended ones.
- **Capability claims backed by scenarios.** A new `true` in a target's
  `Capabilities()` needs a runnable scenario in
  `internal/scenarios/testdata/scenarios/` (prefix = target: `pypsa_`,
  `calliope_`, `calliope06_`, `adopt_`) and a `featureScenarios` entry;
  the coverage test fails the build otherwise.
- **Cross-framework claims checked on real solvers.** When a payload must
  mean the same thing on every target, pin the objective in
  `test/e2e` the way the multi-node and operate-mode checks do.
- **Documentation in the same change.** The site under `docs/` is the
  single source: the capability reference for property-level behaviour,
  the targets page for framework caveats, the changelog for user-visible
  changes. Folder READMEs only point to the site.

## Framework versions

A framework release is a target of its own (see
[ADR-0006](https://enerplanet.github.io/meme/decisions/0006-framework-versions-as-targets/)).
Adding one means: a package implementing `target.Target` (plus
`target.Variant` if it must not join `all`), its own native key in
`model.Native`, a pinned venv and CLI name in `environment/`, scenarios for
its capability claims, goldens, and the E2E lifecycle. Updating a pinned
release is an emitter change, not a pin change: regenerate the goldens and
run the container suite.

## Documentation

The site is MkDocs with the Material theme; `make docs-serve` previews it,
`make docs` builds it strictly (a broken link fails the build). Pages live in
`docs/`; `CHANGELOG.md` and this file are included into the site, so write
links in them as absolute URLs. Keep the capability reference and the
validation gates in step: a changed verdict needs a changed row.

## Releasing

Versions follow [Semantic Versioning](https://semver.org); before 1.0 every
release is a pre-release (`0.1.0-alpha`) and the API and the canonical model
may still change between them. The git tag carries the `v` prefix Go modules
require (`v0.1.0-alpha`); the changelog, `CITATION.cff` and the binary name
the version without it. Nothing in the source changes for a release:
`make build` stamps the version from the tag (`meme -version`,
`GET /healthz`). To cut one from a green `main`:

1. On a `release/<version>` branch, move the *Unreleased* entries of
   `CHANGELOG.md` under `## [<version>] - <date>`, link the heading to the
   release and point *Unreleased* at `compare/v<version>...HEAD`; set
   `version` and `date-released` in `CITATION.cff`. Commit as
   `chore(release): prepare <version>` and land it through a pull request.
2. Tag the landed commit with a signed, annotated tag and push it:
   `git tag -s v<version> -m "MEME <version>" && git push origin v<version>`.
3. Publish the GitHub release from the tag with the changelog section as its
   notes, marked as a pre-release while the version says so:
   `gh release create v<version> --verify-tag --prerelease --title "MEME <version>" --notes-file <notes>`.

## Licensing of contributions

By contributing you confirm that the contribution is your own work (or that
you have the right to submit it) and that it is licensed under the
repository's MIT license.
