# Potential Improvements

- Add workflow that automatically updates the SHA in the action repository on each new release of the container
- ~~Impact analysis only follows `resources`, `bases` and `components`.~~ Now
  specified, along with the other false-pass surfaces, in
  [docs/specs/complete-impact-matching.spec.md](docs/specs/complete-impact-matching.spec.md).

- **Container hardening: move off alpine to reduce CVE surface.** Decided
  direction is a Wolfi/Chainguard base that still ships git, not distroless.
  To be spec'd after complete-impact-matching lands. Research already done,
  do not re-derive:
  - Production code spawns only two binaries: `kustomize` and `git`. Helm is
    invoked by kustomize, not by us. The `git` use is a single command,
    `git diff --name-only base head` (`internal/git/git.go`).
  - Our binary (`CGO_ENABLED=0`), kustomize v5.8.1 and helm v4.2.3 are all
    statically linked, verified with `file`. So a fully distroless image is
    technically possible; git is the only blocker.
  - The `/bin/sh` in `ENTRYPOINT` exists only to expand `${IMAGE_NAME}` and can
    be removed by hardcoding the path.
  - Full distroless would need git replaced by go-git. A working probe showed
    it returns a *superset* of `git diff --name-only` (no rename detection, so
    removed paths that git attributes to a rename are also reported). That is
    safe only because removed paths are now skipped rather than failed. Cost is
    3 modules to 51 and a 4.1 MB binary to 9.4 MB, which is why Wolfi won: it
    trades no dependencies and no behaviour change for most of the CVE benefit.
  - Shallow clones fail under both real git and go-git when the base sha is
    unreachable, so that is not a regression either way. Making the action work
    on `fetch-depth: 1` would be a separate feature.

- **kustomize's vendored dependencies are the remaining CVE surface.** 55 of the
  released image's 65 Trivy findings come from kustomize's own dependencies, and
  10 more from helm's. Both are already on their latest releases, so neither is
  reachable from this repository; they move when upstream updates. Our own binary
  is at zero and CI gates it there. Recorded so the number is not mistaken for
  something this repo can fix. Re-measure with `tests/e2e/scan-baseline.env`.
- **`govulncheck` would be a sharper signal than Trivy for the Go side.** Trivy
  reports every stdlib CVE in a binary; govulncheck reports only the ones actually
  reachable from our code paths. Worth adding if the numbers above ever need
  triage rather than elimination.
