# ADR-0006: Framework versions are separate targets: a default, version variants and aliases

- **Status:** accepted
- **Date:** 2026-10-24

## Context

Calliope 0.7.0 was released as a near-complete rewrite with a model
definition that 0.6 models cannot be loaded into, and a large installed base
still models in 0.6 ([#3](https://github.com/enerplanet/meme/issues/3),
[#4](https://github.com/enerplanet/meme/issues/4)). The two releases have
different capability profiles (0.6 has k-means clustering and a native
emissions objective; 0.7 has custom math, indexed parameters and piecewise
performance), different native vocabularies, and dependency sets that cannot
share one Python interpreter. A single `calliope` target with a version
switch would have to carry two emitters' worth of conditionals and two
incompatible native schemas under one key.

## Decision

A framework may be registered **once per supported release**, each as a full
target (profile, gates, emitter, run plan). The bare name is the default
version (`calliope` = 0.7.0); another release is a **version variant**
(`calliope-0.6`, implementing `target.Variant`) that runs only when named, so
`?target=all` expands to the default targets and never runs two releases of
one framework unless asked. A version-explicit **alias** of the default
(`calliope-0.7` → `calliope`) keeps a pinned spelling valid when the default
moves on and resolves everywhere a target name is accepted. Each target
reports the framework release it drives (`Target.Version`, surfaced by
`GET /capabilities` together with `default_targets` and `aliases`), has its
own native key, and gets its own venv and CLI name in the environment image.

## Consequences

- Both emitters are plain code with no version conditionals; each is tested
  against its own goldens and solved on its own real release in E2E, and the
  same payload is pinned to the same objective on both.
- Existing clients are unaffected: `calliope` and `all` keep their meaning.
  Moving the default to a newer release later means re-pointing one name and
  adding an alias for the old spelling.
- Capability claims are per version, so a payload can be valid for one
  release and rejected for the other with a message that names the other
  target (multi-term constraints: `calliope` yes, `calliope-0.6` no).
- The cost is a second emitter to maintain and a larger golden tree; the
  coverage test and the corpus sweep on both releases keep them honest.
- The alternative — one target, `experiment.framework_version` — was
  rejected because capability profiles, native keys and run commands all
  differ per release; everything the registry keys on is version-specific.
