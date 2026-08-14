---
description: "Verify the kustomize and helm tarballs against SHA256 constants pinned in the Dockerfile as per-arch build ARGs, rather than against a checksum file fetched over the same channel as the tarball."
status: accepted
date: 2026-08-12
author: "SDD Planner (automated), run by michielvha <michielvh@outlook.com>"
---

# ADR-013: How the kustomize and helm tarballs are integrity-verified

## Status

Proposed. Accepted when [plans/container-hardening.md](../plans/container-hardening.md) Phase 3
lands (AC-14).

## Context

`Dockerfile:21-25` and `:29-34` download the kustomize and helm tarballs with `wget` and pipe them
straight onto `PATH`:

```dockerfile
RUN wget -O /tmp/kustomize.tar.gz "https://github.com/.../kustomize_${KUSTOMIZE_VERSION}_linux_${TARGETARCH}.tar.gz" && \
    tar -xzf /tmp/kustomize.tar.gz -C /usr/local/bin && ...
```

There is no verification of any kind. Whatever those URLs serve becomes an executable that the tool
spawns as a child process on every run (`internal/builder/builder.go:78`). Spec F-23 says it
plainly: *a hardening PR that leaves that in place is only half a hardening PR.*

The Dockerfile's download blocks are being rewritten anyway to move `wget` into a builder stage
(F-22), so the marginal cost of adding verification now is close to zero — and the marginal cost of
adding it later is another Dockerfile rewrite and another image release.

## Decision

Verify each tarball with `sha256sum -c` against a **literal SHA256 constant pinned in the
Dockerfile** as a per-tool, per-architecture build `ARG`:

```dockerfile
ARG KUSTOMIZE_SHA256_AMD64=<hex>
ARG KUSTOMIZE_SHA256_ARM64=<hex>
ARG HELM_SHA256_AMD64=<hex>
ARG HELM_SHA256_ARM64=<hex>

RUN case "$TARGETARCH" in \
      amd64) sum="$KUSTOMIZE_SHA256_AMD64" ;; \
      arm64) sum="$KUSTOMIZE_SHA256_ARM64" ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    echo "${sum}  /tmp/kustomize.tar.gz" | sha256sum -c -
```

Four constants. They are obtained from the upstream published checksums at implementation time, and
a comment records for each where it came from and on what date.

Three supporting points:

- **The `*)` arm of the `case` exits non-zero.** An unrecognised `TARGETARCH` must fail the build,
  not silently skip verification. This is the failure mode that turns a checksum into decoration.
- **Version bumps already require editing the Dockerfile** (`KUSTOMIZE_VERSION` at `:20`,
  `HELM_VERSION` at `:28`), so the constants sit next to the thing that invalidates them. Bumping a
  version without bumping its checksum fails the build loudly on the next release, which is the
  correct outcome.
- **The verification runs in the fetcher stage**, which is `--platform=$BUILDPLATFORM` — so
  `sha256sum` executes natively on amd64 even for arm64 targets (see
  [ADR-012](./ADR-012-wolfi-base-and-arm64-package-path.md)).

## Alternatives Considered

| Option | Pros | Cons |
|---|---|---|
| **Chosen: literal SHA256 constants pinned in the Dockerfile** | Real integrity: an attacker must compromise the artifact *and* this repo. Reproducible — the same Dockerfile always installs the same bytes. Fails loudly on a version bump that forgets it. Four lines | Four constants to maintain, one per tool per arch. A `TARGETARCH` this repo does not publish would need a fifth and sixth |
| Fetch the upstream `checksums.txt` / `.sha256sum` and verify against it | Zero constants to maintain; automatically correct on a version bump | Fetched over the **same channel** as the tarball, from the **same origin**. Anyone able to serve a malicious tarball can serve a matching checksum, so this detects corruption but not compromise. It is close to security theatre in a document titled "hardening" |
| Cosign / sigstore verification of the upstream release artifacts | Strongest available guarantee; independent trust root | Requires `cosign` in the fetcher stage plus per-tool trust policy configuration; helm and kustomize publish different provenance shapes. Disproportionate for this change, and the spec explicitly makes even *base* cosign verification out of scope (§9, NF-03) |
| Install kustomize and helm from the Wolfi apk repo instead of tarballs | apk verifies its own package signatures, so integrity comes for free; no download code at all | Would change the shipped **versions** to whatever Wolfi packages, and pinning versions elsewhere is out of scope for this change (spec §9: *"doing both at once makes an AC-4 parity failure ambiguous"*). Worth revisiting as a separate change once parity is established |
| Leave it unverified, as today | No work | Ships an unverified executable in a hardening change. F-23 rejects this |

## Consequences

- **Positive.** The image's three installed binaries all have a verifiable provenance story:
  `entrypoint` comes from GoReleaser's own `dist/`, `kustomize` and `helm` are checksum-pinned, and
  `git` is apk-signed. A corrupted or substituted tarball fails the build rather than shipping.
- **Negative.** Four constants to update whenever `KUSTOMIZE_VERSION` or `HELM_VERSION` changes.
  Mitigated by the fact that the build fails loudly if you forget, and by keeping the constants
  adjacent to the version ARGs.
- **Verification.** AC-14 requires a one-off demonstration: corrupt one constant in a scratch build
  and confirm the build fails. Without that, a `sha256sum -c` that silently never runs looks
  identical to one that passes.
- **Follow-up available.** If the four constants become annoying, the honest upgrade is apk-sourced
  kustomize/helm (row 4), not a same-channel checksum file (row 2).
