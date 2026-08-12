---
description: "Use a digest-pinned cgr.dev/chainguard/wolfi-base for both stages, keep the download work on the build platform, and never carry --no-scripts forward; fall back down a three-rung ladder if emulated apk misbehaves on arm64."
status: proposed
date: 2026-08-12
author: "SDD Planner (automated), run by michielvha <michielvh@outlook.com>"
---

# ADR-012: Wolfi base, digest pinning, and the arm64 package path

## Status

Proposed. Accepted when [plans/container-hardening.md](../plans/container-hardening.md) Phase 4
lands with a real arm64 build recorded in the Dockerfile comment (AC-12).

## Context

`Dockerfile:1` is `FROM alpine:3.23`, and `Dockerfile:15-17` installs `git`, `ca-certificates` and
`wget` with `apk add --no-cache --no-scripts`. The `--no-scripts` flag carries the comment *"avoid
trigger errors with QEMU emulation on ARM64 (Alpine 3.23 issue)"* — direct evidence that the
emulated arm64 package step is already fragile on the current base.

[`docs/specs/container-hardening.spec.md`](../specs/container-hardening.spec.md) moves the base
to Wolfi to reduce the CVE surface. Two things follow that are not settled by the base choice
alone:

1. **`wolfi-base` ships neither `git` nor `wget`** (verified 2026-08-12 by listing every layer of
   the amd64 manifest). `git` is a hard runtime dependency — `internal/git/git.go:41` runs
   `exec.Command("git", "diff", …)` and a start failure aborts the run at `cmd/action/main.go:34-37`.
   So an `apk add git` is unavoidable somewhere, and on arm64 that is an emulated instruction under
   `docker buildx` + QEMU on the amd64 release runner.
2. **Skipping package triggers is itself a correctness risk.** `--no-scripts` was a workaround, not
   a decision. Carrying it onto a new base would import an Alpine-specific defect's workaround into
   an image that may not have the defect, and would leave post-install triggers unrun with no one
   checking what they would have done.

Spec F-20 requires an explicit position, backed by an actual arm64 build.

## Decision

**1. Base.** Both Dockerfile stages use `cgr.dev/chainguard/wolfi-base`, referenced by
`@sha256:<digest>` (F-02). One base for both stages keeps the pin count at one and the layer cache
shared. The digest is re-resolved at implementation time; the digest quoted in the spec
(`sha256:30f0…`) is illustrative only.

Free-tier anonymous pull was verified on 2026-08-12, including for **historical** digests recovered
from the repo's cosign `.sig` tag names — old digests are retained rather than garbage-collected
when `latest` moves, so a pin does not rot. No Chainguard entitlement is purchased.

**2. Keep the downloads off the emulated path.** The fetcher stage is declared
`FROM --platform=$BUILDPLATFORM`, so `wget`, `sha256sum` and `tar` run **natively on amd64** even
when the target is arm64. The stage only downloads and unpacks arch-specific binaries; it never
executes them, so building it on the build platform is sound. This removes every emulated
instruction except one: `apk add --no-cache git` in the runtime stage.

**3. `--no-scripts` is not carried forward, under any outcome.** If the remaining emulated `apk`
misbehaves, the fix is to get off the emulated path, not to disable triggers. Three rungs, tried in
order:

| Rung | Approach | Status |
|---|---|---|
| 1 (default) | `apk add --no-cache git` in the runtime stage, emulated on arm64, no `--no-scripts` | Smallest edit. Wolfi's apk-tools differs from Alpine 3.23's, so the defect may not recur — **must be built, not assumed** |
| 2 | Cross-install from the build-platform fetcher: `apk add --arch aarch64 --root /sysroot --initdb --no-cache git`, then `COPY --from=fetcher /sysroot/ /` | Removes every emulated instruction. **[unverified]** — apk's `--arch`/`--root` capability was not exercised in the planning session |
| 3 | Runtime base becomes `cgr.dev/chainguard/git@sha256:…` — `git` baked in, **no `apk` at all** | Fully verified 2026-08-12: both arches, free tier, `USER 65532`, no `apk` |

Rung 3 needs two compensating changes, both already permitted by the spec:
- `USER 1001:1001` **numerically**, since there is no `adduser` (F-13 explicitly allows this with an
  inline justification).
- `/etc/gitconfig` containing `[safe]\n\tdirectory = *` is **`COPY`-ed in** from the fetcher stage
  instead of produced by `RUN git config --system`. This also removes the dependency on the base
  having a shell, which is `[unverified]` for `chainguard/git`. AC-3 verifies the result either way,
  by reading the config back out of the built image.

**4. The outcome is recorded in the Dockerfile**, in a comment naming the rung used and linking the
arm64 build run that settled it (AC-12). `grep -c -- --no-scripts Dockerfile` must return `0`.

## Alternatives Considered

| Option | Pros | Cons |
|---|---|---|
| **Chosen: wolfi-base for both stages, build-platform fetcher, ladder for `apk add git`** | Smallest diff from the current Dockerfile — `adduser`, `addgroup`, `sh`, `tar`, `chmod`, `chown` and `apk` are all present, so `Dockerfile:15-42` translates almost line for line; F-13 and F-14 keep working identically; one digest to bump | One emulated instruction remains on arm64; the ladder may have to be walked |
| `cgr.dev/chainguard/git` as the runtime base from the start | Zero `apk`, so zero emulation risk; smaller attack surface; `git` maintained by Chainguard | No `adduser`/`addgroup` (numeric `USER` only), no `apk` for future additions, shell presence `[unverified]`, `RUN git config` must become a `COPY`. More unknowns on the first attempt, all of which are then debugged simultaneously with the base swap |
| Carry `--no-scripts` forward onto Wolfi | One-line change; the arm64 build is likely to pass first time | Imports an Alpine-specific workaround into a base that may not need it; leaves post-install triggers unrun with nobody checking what they do; contradicts the spec's own F-20 reasoning that skipping triggers is a correctness risk |
| Keep `alpine:3.23` and just patch packages | No base risk at all | Does not achieve the goal; the CVE surface is the base's, not the packages' |
| Pin the base by tag (`:latest`) instead of digest | No bump maintenance | Repo owner decided otherwise (*"sha is fine its more secure anyway"*); a floating tag makes builds irreproducible and lets the base change under a released image |
| Mirror the base into GHCR under this account | Immune to `cgr.dev` availability (Chainguard offers no registry uptime SLA, NF-04) | Extra moving part for a risk that has not materialised. A digest is mirrorable byte-for-byte, so this stays available as a later fallback without any rework |

## Consequences

- **Positive.** One digest to track; the fetcher's native execution removes most of the emulation
  exposure; the ladder means an arm64 failure has a pre-decided next move instead of an improvised
  one; `--no-scripts` leaves the codebase permanently.
- **Negative.** Rung 1 may fail and cost a day. Rung 2 is unverified. Rung 3 changes two other
  things at once (user creation and git config), which is why it is last.
- **Ongoing.** The digest pin never floats, so it must be bumped deliberately — see
  [ADR-014](./ADR-014-base-digest-freshness.md). An unbumped pin silently re-accumulates the CVEs
  this work exists to remove.
- **Reversible.** Both rung-3 and the alpine base remain one `FROM` line away. The Dockerfile
  rewrite lands as its own commits (plan Phase 3), so `git revert` restores the previous image
  wholesale.
