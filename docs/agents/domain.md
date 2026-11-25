# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`GLOSSARY.md`** at the repo root, or
- **`GLOSSARY-MAP.md`** at the repo root if it exists: it points at one `GLOSSARY.md` per context. Read each one relevant to the topic.
- **`docs/adr/`**: read ADRs that touch the area you're about to work in.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates them lazily when terms or decisions actually get resolved.

Note: this repo's primary design record is the SDD system under `specs/<编号>/` plus `docs/design/HLD.md`, `docs/design/LLD.md`, and the unified architecture in `docs/architecture/README.md`. Read those alongside the glossary/ADRs; when `/domain-modeling` resolves a term or decision, prefer recording it here (glossary/ADR) and linking the owning spec rather than duplicating spec content.

## File structure

Single-context repo:

```
/
├── GLOSSARY.md
├── docs/adr/
│   └── 0001-<decision>.md
├── specs/
└── docs/
```

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `GLOSSARY.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0001 (...), but worth reopening because…_
