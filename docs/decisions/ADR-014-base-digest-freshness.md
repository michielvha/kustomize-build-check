---
description: "Keep the pinned Wolfi base digest fresh with Chainguard's digestabot on a weekly schedule, gated by the new image-check workflow, rather than Renovate or a documented manual cadence."
status: accepted
date: 2026-08-12
author: "SDD Planner (automated), run by michielvha <michielvh@outlook.com>"
---

# ADR-014: Keeping the pinned base digest fresh

## Status

Proposed. Accepted when [plans/container-hardening.md](../plans/container-hardening.md) Phase 6
lands (AC-21). Resolves the spec's **OQ-3**.

## Context

The repo owner chose to pin the base by digest: *"sha is fine its more secure anyway"* (spec §10,
OQ-1). That is the right call for reproducibility — the build is immune to a tag being moved under
it, and a released image's base can be identified exactly.

It also creates the failure mode this whole change exists to prevent. The spec states it directly:

> **F-06's digest-bump mechanism therefore becomes load-bearing** (OQ-3): a pinned digest that is
> never bumped silently re-accumulates the very CVEs this spec exists to remove.

The mechanism is not a nicety attached to the end of the work. It is the difference between a
one-off CVE reduction that decays over months and a durable one. Chainguard's free tier publishes
only `latest` and `latest-dev`, both pointing at the most recent build, so "fresh" means "whatever
`latest` resolves to today" — there is no version-tagged alternative to pin to instead.

Constraints from this repo:

- It has **no Renovate and no Dependabot configuration** today (verified: nothing matching
  `*renovate*` or `*dependabot*` at any depth ≤ 2).
- It has, until this plan, **no pull-request CI at all** — `.github/workflows/` contains only
  `build-release.yml`, which runs on `push: main` and `workflow_dispatch`. A bot that opens PRs is
  only useful if something verifies them.
- A base bump changes the published image, so it must not merge unverified.

## Decision

Add `.github/workflows/digestabot.yml` running Chainguard's **`digestabot`** on a **weekly
`schedule`** plus `workflow_dispatch`. It opens a pull request that updates `BASE_DIGEST` in the
`Dockerfile` to whatever `cgr.dev/chainguard/wolfi-base:latest` currently resolves to.

Chainguard describe it as *"a free GitHub action we created to make it easier for public users to
keep their Chainguard Containers fresh"*
(<https://edu.chainguard.dev/chainguard/containers/staying-secure/updating-images/digestabot.md>).

Three things make the bot safe rather than merely noisy:

1. **Its PRs are gated by `image-check.yml`** — the workflow created in the same plan (Phase 1).
   Every digest bump therefore runs the image-level smoke test (both arches), the container
   contract assertions and the CVE scan **before** anyone merges it. This is the reason the bot is
   worth having at all, and the reason it is added in Phase 6 rather than Phase 1: the gate has to
   exist first.
2. **`README.md` records the contract in prose**: the pin is deliberate, the bot bumps it weekly, a
   bump PR merges once `image-check.yml` is green, and *an unbumped pin is a security regression,
   not a stable state*. The automation can be removed; the expectation should not be lost with it.
3. **AC-21 requires one `workflow_dispatch` run before merge**, reporting either "no update
   available" (expected, since Phase 3 pins a same-day digest) or an opened PR. A scheduled workflow
   nobody has ever run is not a mechanism.

Weekly, not daily: Chainguard rebuild frequently, and daily PRs on a personal OSS repo would be
ignored within a month. Weekly is frequent enough that the pin never drifts far and rare enough to
stay readable.

## Alternatives Considered

| Option | Pros | Cons |
|---|---|---|
| **Chosen: digestabot, weekly, gated by `image-check.yml`** | Purpose-built by the base's own vendor for exactly this registry and this free-tier `latest`-only situation; no new config language; small blast radius | One more scheduled workflow; a third-party action in the repo's trust surface |
| Renovate with `pinDigests` | One tool could cover the base digest **and** the wrapper's `action.yml:45` image pin **and** the Go module; well understood | Requires onboarding Renovate to a repo that has none, plus a `renovate.json` and either the GitHub App or a self-hosted runner. Disproportionate for **one** digest. Genuinely the better answer if this repo ever adopts Renovate for other reasons — revisit then |
| Dependabot `docker` ecosystem | Native to GitHub, zero third-party trust, trivial config | Dependabot's Docker support tracks **tags**; against a registry that publishes only `latest`, it has nothing to move to. Poor fit for a digest-of-`latest` pin |
| Documented manual cadence in `README.md`, no automation | Zero moving parts; explicitly allowed by F-06 | Relies on a single maintainer of a personal OSS project remembering a quarterly chore. This is precisely the mechanism whose failure the spec warns about, and its failure is **silent** — the build stays green while the CVE count climbs back |
| Float the tag (`:latest`, no digest) instead | Always fresh by construction; no bump mechanism needed at all | Rejected by the repo owner. Irreproducible builds, and the base can change under an already-released image without any record of what it was |

## Consequences

- **Positive.** The CVE reduction this plan delivers is maintained rather than decaying. Every bump
  is verified by the same image-level gate that verified the original swap, so a bad base digest is
  caught before merge, not after a release.
- **Negative.** A weekly PR to triage. If it is consistently ignored, the mechanism has failed and
  the honest response is to say so in `README.md`, not to leave a dead workflow implying coverage.
- **Coupling.** This ADR depends on `image-check.yml` existing and running on `pull_request`. If
  that workflow is ever removed, the bot becomes an unverified auto-bump of the published image's
  base — strictly worse than manual. Any change that removes or narrows `image-check.yml` must
  revisit this decision.
- **Not covered here.** The *consumer* pin (`kustomize-build-check-action/action.yml:45`) is bumped
  by hand in plan Phase 7. Automating that is already parked at `TODO.md:1` and is raised as OQ-7 in
  the plan; it is a cross-repo automation with its own token story and is deliberately out of scope.
