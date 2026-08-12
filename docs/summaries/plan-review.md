# Plan Review — pre-implementation

**Date:** 2026-08-12
**Scope:** all four plans in [`docs/plans/`](../plans/) and the ten ADRs in
[`docs/decisions/`](../decisions/), reviewed before any code is written.
**Outcome:** three defects found and fixed, one claim refuted, one accepted as harmless.
Implementation is cleared to start with `complete-impact-matching` Phase 1.

Three plans were authored concurrently by separate agents, so this review focused on the seams
between them rather than on each plan in isolation. That is where the defects were.

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

## Not reviewed

A fan-out review across three lenses (collisions, spec fidelity, adversarial correctness) was
attempted and terminated early on an account spend limit. This review was completed directly and
prioritised the seams between plans and the specific conflicts the planners escalated. **Not
covered in depth:** exhaustive per-plan spec-coverage matrices (does every P0 requirement have an
acceptance criterion) and a systematic spot-check of every `file:line` citation across all four
plans. Those remain worthwhile before the later plans are implemented; they are not blockers for
`complete-impact-matching` Phase 1, whose central change was verified by experiment above.
