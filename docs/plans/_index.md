# Plan Index

Plans are the executable form of a spec: phased, with acceptance criteria that map to tests.
Specs live in [`docs/specs/`](../specs/); architecture decisions live in
[`decisions/`](../decisions/). See [CLAUDE.md](../../CLAUDE.md) for the constitution.

| Plan | Status | Spec | Description |
|------|--------|------|-------------|
| [complete-impact-matching](./complete-impact-matching.md) | **complete** | [complete-impact-matching](../specs/complete-impact-matching.spec.md) | Four phases closing every false-pass class in impact matching: cross-directory reference matching, file-vs-directory classification, the unparseable-kustomization policy, and complete reference-surface parsing. |
| [shallow-clone-support](./shallow-clone-support.md) | **complete** | [shallow-clone-support](../specs/shallow-clone-support.spec.md) | Detect an unresolvable base ref, explain it in one actionable message, then degrade to validating every discovered kustomization instead of crashing. Four phases, diagnostics before behaviour change. |
| [build-timeout-handling](./build-timeout-handling.md) | **complete** | [build-timeout-handling](../specs/build-timeout-handling.spec.md) | Make a timed-out `kustomize build` diagnosable instead of misdiagnosed, make the limit a real wall-clock bound, expose it as a `build-timeout` input, and add the first tests the timeout path has ever had. |
| [container-hardening](./container-hardening.md) | not-started | [container-hardening](../specs/container-hardening.spec.md) | Seven phases moving the released image from `alpine:3.23` to a digest-pinned Wolfi base, behind a new image-level smoke test and a measured before/after CVE scan. Touches no Go code; Phases 1–2 (the harness and the baseline) are recommended to land early, ahead of the behavioural plans. |

**Suggested landing order:** `complete-impact-matching` → `shallow-clone-support` →
`build-timeout-handling` → `container-hardening`. The exception is `container-hardening`
Phases 1–2, which add an image-level E2E harness and touch no Go code, no `Dockerfile` and no
runtime behaviour; landing those first gives the three behavioural plans a pull-request-time gate
that runs the real binary inside the real image. See that plan's *Collisions with other plans*.

## Decisions referenced by these plans

ADR numbers 004–007 were never issued: three plans were authored concurrently and raced for the
same numbers, which was resolved by renumbering rather than by reusing them. The gap is
deliberate, and the numbers are identifiers only.

| ADR | Status | Decision |
|-----|--------|----------|
| [ADR-001](../decisions/ADR-001-graph-reference-classification.md) | proposed | Classify graph references by "is a kustomization discovered here", not by `filepath.Ext` and not by `os.Stat`. Resolves OQ-2. |
| [ADR-002](../decisions/ADR-002-tolerant-kustomization-parsing.md) | proposed | Two-stage `yaml.Node` decode so an undecodable field yields no references instead of dropping the whole file. |
| [ADR-003](../decisions/ADR-003-surfacing-parse-failures.md) | proposed | Apply the always-affected rule in the analyzer; surface parse failures through additive reporter methods, leaving the action outputs contract untouched. |
| [ADR-008](../decisions/ADR-008-fake-command-test-seam.md) | proposed | Inject the build command as an unexported package field and re-exec the test binary through a `TestMain` env gate. |
| [ADR-009](../decisions/ADR-009-waitdelay-grace-injection.md) | proposed | Keep the 5-second `WaitDelay` grace as the production default but hold it in an unexported field rather than a const. |
| [ADR-010](../decisions/ADR-010-e2e-through-the-real-binary.md) | proposed | Exercise `cmd/action` as a built binary from `internal/integration`, so exit codes, `INPUT_*` parsing and `$GITHUB_OUTPUT` get end-to-end coverage. |
| [ADR-011](../decisions/ADR-011-run-metadata-into-the-reporter.md) | proposed | Carry the change-detection mode into the reporter by constructor injection rather than by widening the existing method signatures. |
| [ADR-012](../decisions/ADR-012-wolfi-base-and-arm64-package-path.md) | proposed | Digest-pinned `cgr.dev/chainguard/wolfi-base` for both stages, downloads kept on the build platform, and `--no-scripts` never carried forward — with a three-rung fallback ladder if emulated `apk` misbehaves on arm64. |
| [ADR-013](../decisions/ADR-013-tarball-integrity.md) | proposed | Verify the kustomize and helm tarballs against SHA256 constants pinned as per-arch build `ARG`s, not against a checksum file fetched over the same channel as the tarball. |
| [ADR-014](../decisions/ADR-014-base-digest-freshness.md) | proposed | Keep the pinned base digest fresh with `digestabot` on a weekly schedule, gated by the new image-check workflow. Resolves the container-hardening spec's OQ-3. |
