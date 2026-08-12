---
description: "Surface parse failures through additive reporter methods and an analyzer always-affected rule, leaving BuildResult and the action outputs contract untouched"
status: proposed
date: 2026-08-12
author: "SDD Planner (automated), run by michielvha <michielvh@outlook.com>"
---

# ADR-003: Where the unparseable-kustomization policy lives, and how it is surfaced

## Status

proposed — implements F-C1, F-C2, F-C2a and F-C3 of
[complete-impact-matching.spec.md](../docs/specs/complete-impact-matching.spec.md),
whose OQ-1 is already resolved as option (d), "always affected". Landed in Phase 3 of
[plans/complete-impact-matching.md](../plans/complete-impact-matching.md).

## Context

OQ-1 decided *what* happens: an unparseable kustomization is added to the build set
unconditionally and `kustomize build` adjudicates. It deliberately did **not** decide two
follow-on questions, and both are load-bearing for testability:

1. **Which stage applies the rule?** The union of unparseable directories into the affected
   set could live in `cmd/action/main.go` or in `analyzer.GetAffectedKustomizations`.
2. **How is the parse error surfaced?** F-C3 requires it in the console output and the step
   summary, not just a stderr `Warning:` (`discovery.go:54`), while OQ-1 explicitly rules
   out a fourth result class and any change to the public output contract.

Constraint that decides (1): the E2E harness `internal/integration/pipeline_test.go:113-140`
reproduces the pipeline by calling the stages directly and never executes `main`. Anything
placed in `main.go` is invisible to the repo's only end-to-end layer.

## Decision

**Apply the rule in the analyzer; surface the diagnostics through additive reporter methods.**

**(1) The always-affected rule lives in `analyzer.GetAffectedKustomizations`.** While
iterating `allKustomizations` it treats `kust.Unparsed` as an unconditional match on
`kust.Dir`. The signature, the return type, the absence of an error and the
filesystem-free property are all unchanged (F-E6, NF-02) — `Unparsed` is a field on data
the analyzer is already handed. Placing it here puts the rule inside the E2E harness's
reach for free.

**Unparseable marks the directory itself, not its dependents.** Dependents already
propagate through the ordinary path whenever the unparseable file was actually touched by
the change (`analyzer.go:51-58` plus `GetAllDependents`). Propagating on *every* run would
rebuild the entire downstream tree of a long-broken file on unrelated PRs — the blast
radius OQ-1 rejected when it ruled out option (a). Its graph node still exists (F-C2a),
because `graph.Build`'s first pass creates a node per discovered file, so kustomizations
that reference it keep their edges either way.

**(2) `internal/reporter` gains two additive methods and one value type.**

```go
type ParseIssue struct {
    Path  string // absolute path of the kustomization file
    Field string // "" for a whole-file failure, else the field (ADR-002)
    Err   string
}

PrintParseIssues(issues []ParseIssue)
AppendParseIssuesToStepSummary(issues []ParseIssue) error
```

`ParseIssue` is declared in `reporter` and populated by `main.go` from
`discovery.KustomizeFile`, keeping the dependency direction unchanged (`reporter` does not
import `discovery`). `GenerateSummary`, `PrintResults`, `SetGitHubOutputs` and
`WriteGitHubStepSummary` keep their signatures, so `BuildResult`, the `results` JSON, and
`failed-count` / `success-count` / `skipped-count` are all byte-identical to today. Whole-file
failures and per-field failures (ADR-002 `FieldErrs`) use the same channel, distinguished
by `Field`.

## Alternatives Considered

| Option | Pros | Cons |
|--------|------|------|
| **Rule in the analyzer + additive reporter methods (chosen)** | Covered by the existing E2E harness; no output-contract change; one channel for both failure kinds | `Reporter` grows two methods; `main.go` does a small mapping loop |
| Rule in `cmd/action/main.go` | Keeps the analyzer's inputs "pure" | Invisible to `internal/integration`, so the phase's central behaviour would have no E2E coverage. Disqualifying under the plan's per-phase E2E requirement. |
| Rule in `discovery.FindAll` (return the set pre-marked) | Closest to the data | Discovery does not know about the affected set; would invert the pipeline's direction of flow |
| Synthesise a failed `BuildResult` for the parse error | Reuses the existing report sections with zero new methods | Reintroduces a synthetic failure that is not a `kustomize build` outcome, contradicting OQ-1's "kustomize adjudicates" and inflating `failed-count` |
| Extend `WriteGitHubStepSummary(results, issues)` | One method instead of two | Breaking signature change on the interface for no benefit; the two concerns are written at different points in `main` |
| New action output `unparseable-count` | Machine-readable | OQ-1 explicitly rejected changing the public output contract |

## Consequences

**Positive**

- G3 cannot end in a green check: the directory is always built, and `kustomize build`
  decides (F-C1, F-C2).
- No new result class, no new action output, nothing for consumers to migrate.
- `discovery.go:54`'s direct `fmt.Fprintf(os.Stderr, ...)` is deleted, satisfying NF-05.

**Negative / accepted**

- One extra `kustomize build` per unparseable file per run. Rare and bounded; explicitly
  accepted in OQ-1 as the false-fail side of the asymmetry.
- The E2E harness's `run()` helper must be extended to invoke the reporter's step-summary
  writer against a temp `GITHUB_STEP_SUMMARY` so AC-C2 is assertable end to end. This
  widens what the harness covers, which is a benefit, but it is a change to shared test
  infrastructure that every later phase inherits.
- A directory can now be built even when nothing in the diff touched it. `TestModifiedDirectoryStillBuilds`
  and the set-equality assertions of F-E4 must therefore keep their fixtures free of
  malformed YAML, or state it deliberately.
