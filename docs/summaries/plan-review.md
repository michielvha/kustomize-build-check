# Plan Review — pre-implementation

**Date:** 2026-08-12
**Scope:** all four plans in [`docs/plans/`](../plans/) and the ten ADRs in
[`docs/decisions/`](../decisions/), reviewed before any code is written.
**Outcome:** two rounds. Round 1 (direct, partial) found three defects. Round 2 (full three-lens
fan-out) found seven more including **two new false-pass classes**, and caught a defect round 1's
own fix had missed. All blocking findings are fixed; spec amendments and lower-severity items are
recorded below as an explicit punch list.

**Implementation is cleared to start with `complete-impact-matching` Phase 1**, whose central
change was independently re-verified by experiment in both rounds. Phases 2 and 3 gained scope
(AC-B7), and `shallow-clone-support` Phase 3 is **blocked** until the discovery fix in R2-2 lands.

Three plans were authored concurrently by separate agents, so this review focused on the seams
between them rather than on each plan in isolation. That is where nearly every defect was.

## Findings

### 1. AC-C7 would have started failing for an unrelated reason — FIXED

`complete-impact-matching` AC-C7 asserted `SetGitHubOutputs` emits "exactly the same four keys".
`shallow-clone-support` adds a fifth (`change-detection-mode`) and lands immediately after.

The subtlety is that this is not a sequencing bug: plan 1 lands first, so AC-C7 is *true* when it
is verified, and only becomes false later. A literal four-key assertion would pass its own review
and then fail for a reason unrelated to the plan that wrote it.

Reworded to assert **additivity** — this phase adds no key and changes no existing key's shape —
which is the invariant that actually matters. The Test Plan row was updated to match, so the
assertion is "the four keys are present with unchanged shapes", not "these are the only keys".

### 2. `build-timeout-handling`'s collision guidance was stale — FIXED

It was written while `shallow-clone-support` was still in flight and recorded:

> plan 2 changes the signatures of the two methods this plan edits the bodies of. Land plan 2
> first, then write Phase 2 against the new signatures.

That is no longer true. [ADR-011](../decisions/ADR-011-run-metadata-into-the-reporter.md) chose
constructor injection (`reporter.New(RunInfo)`) *specifically* so all four method signatures stay
byte-identical. An implementer following the stale text would have gone looking for a signature
that never changes.

Corrected in place, with the superseded assumption left visible. The real residual contact is
smaller: the `reporter.New()` constructor gains a parameter. Landing order is unchanged, but now
for the right reason (avoiding a rebase of call sites, not adapting to a new signature).

### 3. Plan 1 never mentioned the release or the consumer pin — FIXED

`complete-impact-matching` had no release step, while the other three plans all did. Every one of
its phases changes Go code, so merging triggers `build-release.yml` and publishes a new image —
but the wrapper action pins by digest, so **until `action.yml`'s `image:` pin is bumped, no
consumer sees any of the work**. `@main` keeps serving the old image.

This is not hypothetical: the same step was needed manually after PR #8.

Added as Phase 5, with the instruction to verify the tag exists in GHCR before bumping, and to
bump once after the last plan lands rather than per-plan.

### 4. "The input-ordering rule is only in one plan" — ACCEPTED, harmless

`build-timeout-handling` requires its `build-timeout` validation to run *above*
`shallow-clone-support`'s `ResolveBase`, so a config typo does not first pay for git I/O. The rule
appears only in `build-timeout-handling`, not in `shallow-clone-support`.

That is fine. `build-timeout-handling` lands second of the two and is the plan that has to do the
inserting, so the knowledge lives where it is needed. No change made.

### 5. De-duplication of the full-scan set — VERIFIED PRESENT

`shallow-clone-support`'s `validate-all` fallback builds every discovered kustomization, and
`complete-impact-matching` Phase 3 changes what `FindAll` returns by retaining unparseable files.
The interaction is handled: de-duplication is called out as load-bearing rather than cosmetic,
because `FindAll` can already emit two entries for one directory and the retained entries add
another route to the same `Dir`.

## Verified independently, by experiment

- **Phase 1's guard removal is safe.** Probed against the real binary before planning: `base` is
  matched through its `../shared/cm.yaml` reference, `overlays/dev` propagates as a transitive
  dependent, and sibling `base-old/` is **not** dragged in — the resources loop's resolved-path
  matching is already precise enough on its own. The full suite (31 tests) passed under the probe,
  which was then reverted.
- **G5 is real and is a regression from PR #8.** Deleting a base outright while an overlay still
  references it reports `1 total, 0 failed, 1 skipped`, "All builds successful", exit 0, while
  `kustomize build overlays/dev` genuinely fails. Phase 1 closes it.

## Judgement call, no change needed

`TestExtractDependencies` asserts "expected 3 dependencies"; Phases 1+2 make it 5, and the plan
updates the number. This is a legitimate update rather than the erasure of a signal: the count is
an assertion on an *intermediate value*, not on behaviour, and the plan pairs the update with a
new test asserting the three edges actually produced. The behavioural assertion is strengthened
while the incidental one is corrected.

## Round 2 — full three-lens fan-out

The fan-out that was cut short by a spend limit was re-run in full: cross-plan collisions, plan-to-
spec fidelity with exhaustive P0 coverage matrices and ~95 citation spot-checks, and adversarial
correctness with live probing. It found substantially more than round 1, including **two new
false-pass classes**, and it caught a defect round 1's own fix had missed.

### Fixed in this round

| # | Finding | Severity |
|---|---|---|
| R2-1 | **B4 / OQ-6 was a reproducible false pass parked as non-blocking.** A base whose directory held only `kustomization.yaml`, deleted while an overlay still references it: exit 0 against a genuinely broken repo. Phase 1 does **not** close it — the only changed path is the kustomization file itself, so widened matching has nothing under the directory to catch. The stated reason for deferring ("the complete fix is a pre-change-tree graph, out of scope") was **factually wrong**: `graph.go:74-84` only records the reverse edge when a node was discovered at the resolved path, and moving that append outside the `if exists` guard closes it in ~3 lines with all 31 tests green. Promoted into Phase 2 with AC-B7. | **Blocking** |
| R2-2 | **B1: `full-scan` is not a superset of `diff`, so shallow-clone Phase 3 would introduce a new false pass.** `analyzer.go:51-58` adds a directory by basename **without consulting the discovered set**, while `discovery.go:44-47` skips *every* dot-prefixed directory. A kustomization under `.deploy/` is validated in `diff` mode and invisible to `full-scan`: verified, `diff` exits 1 and `full-scan` exits 0 on the same broken commit pair. Since today's shallow run crashes with exit 1, Phase 3 would turn a red run green. Fix belongs in discovery (skip `.git`, not all dot-dirs), never in the analyzer — narrowing the analyzer would destroy a real detection. Added AC-P13; noted that AC-12 currently passes vacuously. | **Blocking** |
| R2-3 | **`build-timeout-handling` AC-21 had the identical defect round 1 fixed in AC-C7** — "exactly the four existing outputs", which `shallow-clone-support`'s fifth output breaks. Round 1 fixed one instance and missed the other. Reworded to assert additivity. | **Blocking** |
| R2-4 | **`reporter.New()` has six call sites, not three.** The plan and ADR-011 both undercount. The three missing are in `internal/reporter/reporter_test.go` (`:24`, `:46`, `:69`) and are **compile-breaking** against a changed constructor. | **Blocking** |
| R2-5 | **Round 1's Phase 5 fix created a cross-plan conflict.** It claimed the other three plans also bump the wrapper pin once at the end; two of them assume the opposite, and `container-hardening` AC-13 (identical counts across the bump) is *guaranteed* to fail if several plans ride one bump. It also created the stack's only forward dependency. Corrected to bump per plan. | **Blocking** |
| R2-6 | Stale statements contradicting round 1's own corrections: `build-timeout-handling` still told the implementer to write against "widened reporter signatures" in two places outside the corrected section; `shallow-clone-support` still quoted AC-C7's superseded wording and escalated a closed question. | High |
| R2-7 | **`shallow-clone-support` OQ-7 recommended moving the SDD artifacts *back* to root** — stale, and actively dangerous now that `docs/` is settled: acting on it would break `CLAUDE.md`'s reviewer write scope and the release trigger. Marked resolved with an explicit do-not-act warning. | High |

### Verified as correct, no action

- The containment-guard deletion (Phase 1) is safe in both directions, re-confirmed independently:
  `base-old/` never marks `base/`, the intended cross-directory match is gained, and G5 closes.
  31 tests pass with the guard removed.
- Both timeout defects reproduce exactly: the nil-`cmd.Process` panic fires unrecovered in the
  `AfterFunc` goroutine, and the wall-clock measurements land within a hundredth of the plan's
  figures (30.01s / 5.30s / 0.80s). Notably `CommandContext` **without** `WaitDelay` is still
  unbounded at ~30s, so ADR-009's grace is load-bearing rather than an optimisation.
- `container-hardening`'s fixture really is plan-neutral across every phase of every other plan,
  so pulling Phases 1–2 forward will not bake in expectations that later break.
- No two ADRs make incompatible decisions; the 004–007 gap is cosmetic.
- `BuildResult` is touched by exactly one plan, additively.
- The `TestExtractDependencies` 3→5 change is a legitimate update to an intermediate-value
  assertion — with the caveat that after it the assertion is tautological, so the paired
  edge-asserting test carries all the signal and its fixture must not be vacuous.

### Round 3 — closing the gap between "recorded" and "applied"

Everything in the round-2 punch list below has now been **applied**, not just recorded. Re-checking
my own work against the review also surfaced **two blocking adversarial findings I had skipped**,
both verified on `main` before fixing:

| # | Finding | Severity |
|---|---|---|
| R3-1 | **An *unreadable* kustomization is a silent false pass, and Phase 3 was going to make it worse.** F-C1 says "cannot be **read or** parsed", but the read error happens at `discovery.go:71-74`, *before* the YAML stage, so nothing set `Unparsed` for it. Phase 3 deletes the stderr warning, turning today's *noisy* false pass into a *silent* one. Verified: a dangling-symlink `kustomization.yaml` with a sibling file edited reports "No kustomizations affected", exit 0, while `kustomize build` fails. Fixed, with AC-C8. | **Blocking** |
| R3-2 | **`FieldErrs` made AC-C4 unsatisfiable as written.** A field the tool cannot decode silently removed that directory's whole reference surface, and only `Unparsed` triggered the always-affected rule. AC-C4 asserts the directory "reaches `kustomize build`"; nothing put it back. A `FieldError` is the same epistemic position as `Unparsed` — the tool does not know what the field referenced — so it now triggers the rule too. Fixed, with AC-C9. ADR-002's "one and only condition" sentence, which made both gaps look deliberate, is corrected. | **Blocking** |
| R3-3 | **Nobody owned the shared `run()` helper.** Three plans reshape it and none was responsible for the final signature. `complete-impact-matching` lands first, so it now owns it explicitly and records the resulting shape. | Medium |

The lesson repeats: R3-1 is *again* a locally-correct change (route diagnostics through the
reporter) removing the accident that was catching something.

### Round-2 punch list — all now applied

**Spec amendments needed** (the plans are right, the specs are wrong):

- `complete-impact-matching` **F-C3** requires the parse error to surface through *action outputs*,
  which AC-C7 and OQ-1's resolution forbid. The spec contradicts itself; amend F-C3 to require
  console + step summary only.
- `shallow-clone-support` **F-21 vs F-10**: the `fail` path exits before discovery, so neither
  `diff` nor `full-scan` is true. Amend F-21 to exempt that path explicitly. Also F-10 mis-cites
  `F-14/F-15` for the message body; it should be `F-17/F-18`. The plan also owes itself an AC
  asserting no outputs are written on that path.
- `container-hardening` **NF-05** is factually false — `git` arrives via `apk` dynamically linked,
  and that was already true of today's alpine image. Amend to the two-part invariant.
- `build-timeout-handling` **F-10**'s amendment is currently scheduled *after* implementation,
  inverting "specs are the source of truth". It should land before Phase 1, and an AC is needed
  pinning production `grace == 5s`, which nothing currently does once it stops being a `const`.

**P0 requirements with no acceptance criterion:** `container-hardening` F-16 (no hardcoded image
name — the one thing `CLAUDE.md` makes non-negotiable; the current AC-1 would pass an entrypoint
that hardcodes it), F-01, F-04, F-05, F-19; `complete-impact-matching` F-E6 (the analyzer output
contract); `build-timeout-handling` F-11, F-21.

**Other:** every plan overstates "an E2E row per phase" (gaps in CIM Phase 5, SCS Phases 1 and 4,
BTH Phase 4, CH Phases 3 and 6); `container-hardening`'s `image-check.yml` `paths:` filter means
the pull-forward gate never fires on the behavioural plans' PRs, contradicting its own rationale;
`build-timeout-handling` builds a second real-binary harness duplicating ADR-010; three plans
reshape the shared `run()` helper with no owner for the final signature; contradictory guidance for
where the second lander inserts its `getEnv` and `action.yml` entries; `container-hardening` Phase 7
hardcodes today's pin SHA; ~11 citation errors, mostly a systematic one-line drift in
`container-hardening`'s `build-release.yml` references.

### The pattern worth remembering

Both rounds found the same failure shape the code itself keeps producing: **a locally-correct
change that removes the accident which was catching something.** G5 was a correct skip guard that
deleted a bogus failure doing real work. R2-1 and R2-2 are the same shape at the plan level, and
R2-3 and R2-5 are round 1's own fixes doing it. Every fix here was checked for what it might be
quietly removing, not only for what it adds.

## Coverage

Round 1 was completed directly after the fan-out hit an account spend limit, and covered only the
seams between plans plus the conflicts the planners had escalated. **Round 2 closed that gap**: the
three lenses ran in full, including exhaustive P0 coverage matrices for all four spec/plan pairs,
~95 `file:line` citation spot-checks, and live adversarial probing against the real binary.

The feared citation staleness did not materialise — every citation into the regions that changed
during this work (`skipReason`, the analyzer's path normalisation, the reporter's `Skipped`
handling) is correct. `build-timeout-handling`'s citations are flawless.

What remains unreviewed is small and named: the spec amendments and P0-coverage gaps in the
punch list above are recorded, not resolved. None blocks Phase 1.
