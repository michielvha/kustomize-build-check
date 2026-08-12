---
description: "Carry the change-detection mode into the reporter by constructor injection (reporter.New(RunInfo)) rather than by widening SetGitHubOutputs and WriteGitHubStepSummary"
status: proposed
date: 2026-08-12
author: "SDD Planner (automated), run by michielvha <michielvh@outlook.com>"
---

# ADR-011: How the change-detection mode reaches the reporter

## Status

proposed — implements F-21, F-22 and F-24 of
[shallow-clone-support.spec.md](../specs/shallow-clone-support.spec.md). Landed in
Phase 3 of [plans/shallow-clone-support.md](../plans/shallow-clone-support.md).

## Context

F-21 requires a new action output `change-detection-mode` written to `$GITHUB_OUTPUT` on
**every** run, including the zero-affected-paths early return (`main.go:63-76`). F-22
requires the same fact in `$GITHUB_STEP_SUMMARY`, again including that early return. F-24
names the mechanical consequence — the mode has to reach `SetGitHubOutputs` and
`WriteGitHubStepSummary` (`reporter.go:24-29`) — and explicitly leaves the shape to the
planner: *"The shape is the planner's call; the requirement is that the mode reaches both
sinks."*

Two constraints narrow the choice.

**1. Silent degradation is the failure mode this feature exists to prevent.** The spec's own
risk list puts it above everything except over-triggering: if the mode is droppable, a
degraded run looks identical to a normal one. Whatever shape is chosen should make "forgot to
pass the mode" hard rather than merely discouraged.

**2. `internal/reporter` is touched by another in-flight plan.**
[plans/complete-impact-matching.md](../plans/complete-impact-matching.md) Phase 3 adds
`ParseIssue`, `PrintParseIssues` and `AppendParseIssuesToStepSummary`, and its
[ADR-003](../decisions/ADR-003-surfacing-parse-failures.md) deliberately keeps
`GenerateSummary`/`PrintResults`/`SetGitHubOutputs`/`WriteGitHubStepSummary` at their current
signatures — its AC-C7 asserts that `SetGitHubOutputs` "emits exactly the same four keys with
the same shapes as before the phase". ADR-003's own alternatives table rejects
`WriteGitHubStepSummary(results, issues)` as a *"breaking signature change on the interface
for no benefit"*. A plan that widens those same two methods therefore collides with the plan
scheduled to land immediately before it, at the level of both text and stated intent.

The `Reporter` is constructed twice in `main.go` (`:66` and `:89`) and once in the E2E
harness (`pipeline_test.go:139`) — three call sites, against four method call sites
(`:67, :70, :93, :98`).

## Decision

**Inject the run metadata at construction: `reporter.New(RunInfo)`. Leave all four method
signatures exactly as they are.**

```go
// internal/reporter

type Mode string

const (
    ModeDiff     Mode = "diff"
    ModeFullScan Mode = "full-scan"
)

// RunInfo is the context of one run, fixed before any result exists.
type RunInfo struct {
    Mode    Mode   // "diff" | "full-scan"; published as change-detection-mode (F-21)
    BaseRef string // the effective base ref that was probed (F-01)
    Reason  string // "" in diff mode; the one-line cause in full-scan mode (F-22)
}

func New(info RunInfo) Reporter
```

- `SetGitHubOutputs` appends `change-detection-mode=<info.Mode>` as a **fifth** line, after
  the existing four, whose names, meanings and emission order are untouched (NF-07).
- `WriteGitHubStepSummary` writes the mode as a row of the existing metrics table and, when
  `Mode == ModeFullScan`, a short block naming `info.BaseRef` and `info.Reason` — placed
  immediately after the metrics table and before the `### ❌ Build Errors` section, so its
  position is fixed and does not contend with the parse-issue section
  [ADR-003](../decisions/ADR-003-surfacing-parse-failures.md) appends.
- Both early-return call sites (`main.go:66-72`) construct the reporter with the same
  `RunInfo`, so F-21's "including the zero-affected-paths early return" is satisfied by the
  fact that there is no way to obtain a `Reporter` without one.
- **Unset `Mode` resolves to `ModeFullScan`, not to `ModeDiff`, and logs `slog.Error`.** An
  unset mode can only be a programming error; of the two wrong answers, "claims it degraded
  when it did not" is noisy and self-correcting, while "claims a normal run when it degraded"
  is the invisible degradation the feature exists to prevent. This is the same asymmetry
  CLAUDE.md applies to build results, one level up.

## Alternatives Considered

| Option | Pros | Cons |
|--------|------|------|
| **Constructor injection, `reporter.New(RunInfo)` (chosen)** | F-21/F-22 hold by construction — a `Reporter` cannot exist without a mode; three call sites change instead of four; **all four method signatures are untouched, so the collision with ADR-003 and with complete-impact-matching's AC-C7 disappears**; the mode is genuinely per-run state, not per-call | `reporter.New()` gains a parameter, which touches the E2E harness (`pipeline_test.go:139`); the `Reporter` becomes stateful, which it was not |
| Widen both methods: `SetGitHubOutputs(results, info)` / `WriteGitHubStepSummary(results, info)` (the shape F-24 sketches) | Explicit at every call site; reporter stays stateless | Breaks the `Reporter` interface at exactly the two methods ADR-003 promised to leave alone, and falsifies a literal reading of complete-impact-matching's AC-C7 ("exactly the same four keys"); four call sites; a caller can still pass a zero `RunInfo` |
| Add parallel methods `SetGitHubOutputsWithInfo` / `WriteGitHubStepSummaryWithInfo` | Zero breakage anywhere | Two ways to do one thing, and the mode-less versions stay callable — F-21's "on every run" becomes a convention instead of an invariant. Rejected on the same grounds as the chosen default for `Mode`. |
| A package-level `reporter.SetMode()` | Smallest diff of all | Global mutable state in a library package; untestable in parallel; invisible at call sites |
| Pass the mode as a field on every `builder.BuildResult` | Rides the existing wire | Wrong cardinality — the mode is one value per run, not per result — and it would change the `results` JSON schema, which NF-07 forbids. Also collides with build-timeout-handling, which adds real fields to `BuildResult`. |
| Have the reporter probe the mode itself | No plumbing | The reporter would have to run git, inverting the pipeline's direction of flow and making it untestable without a repository |

## Consequences

**Positive**

- The two P0 visibility requirements are structurally enforced rather than reviewed for.
- `internal/reporter`'s method set is byte-identical to what complete-impact-matching's
  Phase 3 leaves behind, so the only merge contact between the two plans in this file is the
  `New` signature and one new table row — a mechanical resolution rather than a semantic one.
- `RunInfo.BaseRef` and `RunInfo.Reason` give the step summary the same content as stderr
  without `main.go` formatting the message twice.

**Negative / accepted**

- Three constructor call sites change, one of them in shared test infrastructure that
  complete-impact-matching also edits. That is one conflicting line in `pipeline_test.go`.
- `Reporter` holds state for the first time. Bounded: `RunInfo` is set once and never
  mutated, and the reporter has no other fields.
- A fifth key appears in `$GITHUB_OUTPUT`. Additive and declared in both `action.yml` files
  (F-16, NF-06, NF-07), but any consumer parsing that file positionally rather than by key
  would be affected. No such consumer is known. **[unverified — consumer workflows outside
  these two repositories were not inspected.]**
- The `on-unresolvable-base: fail` path exits before a reporter is constructed (F-10 requires
  exiting *before* discovery), so it writes no outputs at all — see the plan's Open Questions,
  which escalates this deviation from a literal reading of F-21.
