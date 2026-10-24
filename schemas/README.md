# `schemas/` — the payload contract

| File | Contents |
|---|---|
| [`revised_unified_schema.json`](revised_unified_schema.json) | **The published contract** for the canonical `Job` payload (JSON Schema 2020-12). `make schema-check` validates every example and every scenario of the corpus against it. |
| [`unified_schema.json`](unified_schema.json) | The framework-neutral unification of the three frameworks' configurations the contract was derived from, with `x-map` annotations naming each property's native counterpart. |
| [`pypsa_schema.json`](pypsa_schema.json), [`calliope_schema.json`](calliope_schema.json), [`adopt_net0_schema.json`](adopt_net0_schema.json) | The per-framework input schemas (PyPSA 1.2.4, Calliope 0.7, AdOpT-NET0 0.1.10) the unified schema references; also the vocabulary for the `native` blocks. |
| [`unified_schema_vs_go.md`](unified_schema_vs_go.md) | The reconciliation report between the unified schema and the Go model. |

What each target honors, property by property, is the **capability
reference**: <https://enerplanet.github.io/meme/reference/capability-reference/>
(source: `docs/reference/capability-reference.md`).
