# Plan Index

Plans are the executable form of a spec: phased, with acceptance criteria that map to tests.
Specs live in [`docs/specs/`](../docs/specs/); architecture decisions live in
[`decisions/`](../decisions/). See [CLAUDE.md](../CLAUDE.md) for the constitution.

| Plan | Status | Spec | Description |
|------|--------|------|-------------|
| [complete-impact-matching](./complete-impact-matching.md) | not-started | [complete-impact-matching](../docs/specs/complete-impact-matching.spec.md) | Four phases closing every false-pass class in impact matching: cross-directory reference matching, file-vs-directory classification, the unparseable-kustomization policy, and complete reference-surface parsing. |

## Decisions referenced by these plans

| ADR | Status | Decision |
|-----|--------|----------|
| [ADR-001](../decisions/ADR-001-graph-reference-classification.md) | proposed | Classify graph references by "is a kustomization discovered here", not by `filepath.Ext` and not by `os.Stat`. Resolves OQ-2. |
| [ADR-002](../decisions/ADR-002-tolerant-kustomization-parsing.md) | proposed | Two-stage `yaml.Node` decode so an undecodable field yields no references instead of dropping the whole file. |
| [ADR-003](../decisions/ADR-003-surfacing-parse-failures.md) | proposed | Apply the always-affected rule in the analyzer; surface parse failures through additive reporter methods, leaving the action outputs contract untouched. |
