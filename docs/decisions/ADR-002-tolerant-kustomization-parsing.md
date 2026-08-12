---
description: "Parse kustomization files with a two-stage yaml.Node decode so an undecodable field yields no references instead of dropping the whole file"
status: proposed
date: 2026-08-12
author: "SDD Planner (automated), run by michielvha <michielvh@outlook.com>"
---

# ADR-002: Tolerant per-field kustomization parsing

## Status

proposed — implements F-C5, F-C6 and NF-06 of
[complete-impact-matching.spec.md](../specs/complete-impact-matching.spec.md).
Landed in Phase 3, consumed by Phase 4 of
[plans/complete-impact-matching.md](../plans/complete-impact-matching.md).

## Context

`ParseKustomization` decodes into one anonymous struct and returns an error if any field
fails (`discovery.go:76-84`). `FindAll` turns that error into a stderr warning and drops the
file (`discovery.go:51-56`), so the kustomization is absent from the graph and from the
analyzer's input while the run still exits 0 — false-pass class **G3**.

Today the blast radius is small because only three `[]string` fields are decoded. Phase 4
raises it sharply: `patches`, `configMapGenerator`, `secretGenerator`, `helmCharts`,
`patchesJson6902` and `openapi` are all structured, and each one is a new way for a single
odd value to drop an entire file. Left alone, the phase that closes G2 would **enlarge** G3.

The repo has exactly one direct dependency, `gopkg.in/yaml.v3` (`go.mod:4`), and
`CLAUDE.md` requires walking the reuse ladder before adding a second. NF-06 names
`yaml.Node` / a two-stage decode as the intended route and forbids a schema library or a
kustomize API import.

## Decision

**Decode in two stages, both on `gopkg.in/yaml.v3`.**

**Stage 1 — document shape only.** Unmarshal into a struct whose every field is a
`yaml.Node`. This succeeds for any well-formed YAML mapping regardless of what the values
look like, and it stays non-strict, so unknown fields are still ignored (F-C5). Only
genuinely malformed YAML fails here, and that failure is the one and only condition that
marks a file `Unparsed`.

```go
type rawKustomization struct {
    Resources             yaml.Node `yaml:"resources"`
    Bases                 yaml.Node `yaml:"bases"`
    Components            yaml.Node `yaml:"components"`
    Patches               yaml.Node `yaml:"patches"`
    PatchesStrategicMerge yaml.Node `yaml:"patchesStrategicMerge"`
    PatchesJSON6902       yaml.Node `yaml:"patchesJson6902"`
    ConfigMapGenerator    yaml.Node `yaml:"configMapGenerator"`
    SecretGenerator       yaml.Node `yaml:"secretGenerator"`
    HelmCharts            yaml.Node `yaml:"helmCharts"`
    CRDs                  yaml.Node `yaml:"crds"`
    Configurations        yaml.Node `yaml:"configurations"`
    OpenAPI               yaml.Node `yaml:"openapi"`
}
```

**Stage 2 — one independent `node.Decode(&typed)` per field.** A field that is absent
(`node.Kind == 0`) contributes nothing. A field that fails to decode contributes nothing,
records a `FieldError{Field, Err}`, and leaves every other field untouched. The file is
retained with whatever references the other fields yielded (F-C6).

Every reference produced by stage 2 is a `Ref{Raw, Path, Field}`, where `Field` is the
provenance string (`"patches[].path"`, `"configMapGenerator[].files"`) that satisfies F-D11
and feeds the debug line required by F-A5.

**Field-level tolerance is per field, not per entry.** A `patches` list whose third entry
is malformed loses the whole `patches` field, not just that entry. This is the simpler
contract and stays on the safe side: fewer references learned means the directory is
matched less often, and Phase 3's always-affected policy is not what catches that — so the
plan pairs it with the rule that any `FieldError` is reported through `internal/reporter`
(ADR-003) rather than only logged.

## Alternatives Considered

| Option | Pros | Cons |
|--------|------|------|
| **Two-stage `yaml.Node` decode (chosen)** | Zero new dependencies; exactly NF-06's named route; failure is contained to one field; provenance falls out naturally | Slightly more code than one struct; `yaml.Node` handling is less obvious to a reader than plain tags |
| One flat struct with all fields typed | Smallest diff; familiar | One bad value drops the whole file — this is precisely the defect being fixed. Directly violates F-C6. |
| `map[string]any` and hand-walk it | No new dependency; maximum tolerance | Re-implements decoding by hand for eight field families; every shape becomes a type-switch; far more code to get wrong than `node.Decode` |
| Import `sigs.k8s.io/kustomize/api/types` for the real `Kustomization` struct | Always exactly right about field shapes | Adds a large second dependency and its transitive tree, against `CLAUDE.md` and F-E7. Also couples this tool's parse strictness to kustomize's, which OQ-1 deliberately decoupled. |
| Per-entry tolerance inside a list | Loses even less information | Needs a `[]yaml.Node` intermediate and per-entry error accounting for eight surfaces; disproportionate for a case that has not been observed |

## Consequences

**Positive**

- Phase 4 can add eight reference surfaces without adding eight whole-file-drop modes.
- `Unparsed` becomes a narrow, well-defined state — malformed YAML only — which is exactly
  what Phase 3's always-affected policy (F-C2) is calibrated for.
- Provenance (F-D11) and the alias split (F-D4) both live in one place, `Ref`.

**Negative / accepted**

- `discovery.KustomizeFile` grows `FileRefs []Ref`, `Unparsed bool`, `ParseErr string`,
  `FieldErrs []FieldError`. `Resources`, `Bases` and `Components` are retained unchanged so
  `graph.Build` keeps compiling (NF-08); the `resources` entries appear in **both**
  `Resources` (directory candidates for the graph) and `FileRefs` (file candidates for the
  analyzer), which is the two-separate-decisions rule F-B3 requires.
- One extra decode pass per kustomization file. Bounded by file count, not by changed-file
  count; immaterial next to spawning `kustomize build`.
- `ParseKustomization` keeps its `(*KustomizeFile, error)` signature but now returns a
  non-nil `*KustomizeFile` **with** the error for the malformed-YAML case, so `FindAll` can
  honour F-C2a. Callers that ignore the value on error are unaffected; the one in-repo
  caller is updated in the same phase.
