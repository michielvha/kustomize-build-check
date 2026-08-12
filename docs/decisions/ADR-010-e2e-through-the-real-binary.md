---
description: "Exercise cmd/action as a built binary from internal/integration, so exit codes, INPUT_* parsing, $GITHUB_OUTPUT and stderr diagnostics have end-to-end coverage"
status: proposed
date: 2026-08-12
author: "SDD Planner (automated), run by michielvha <michielvh@outlook.com>"
---

# ADR-010: The integration harness runs the real `cmd/action` binary

## Status

proposed — resolves F-30 of
[shallow-clone-support.spec.md](../specs/shallow-clone-support.spec.md) and is the
enabling change for AC-1, AC-3, AC-4, AC-7, AC-8, AC-13 and AC-24. Landed in Phase 2 of
[plans/shallow-clone-support.md](../plans/shallow-clone-support.md).

## Context

`internal/integration/pipeline_test.go` is this repo's E2E layer, but it does not run the
program. `run()` (`pipeline_test.go:113-140`) re-implements the pipeline stage by stage —
`git.New().GetChangedFiles` → `discovery.FindAll` → `graph.Build` →
`analyzer.GetAffectedKustomizations` → `builder.BuildAll` → `reporter.GenerateSummary` — and
returns a `reporter.Summary`. `main()` is never called.

Everything this feature adds lives outside that reproduction:

| What the spec requires | Where it lives | Covered by `run()` today |
|---|---|---|
| `INPUT_ON-UNRESOLVABLE-BASE` parsing and the unrecognised-value rule (F-14, F-15) | `main.go` `getEnv` block | no |
| The preflight branch and the choice of candidate set (F-07..F-10) | `main.go` orchestration | no |
| Exit code 1 vs 0 (F-10, §6.4, AC-1, AC-7, AC-24) | `os.Exit` in `main.go` | no — `run()` returns a struct |
| `change-detection-mode` in `$GITHUB_OUTPUT` (F-21, AC-3) | `reporter.SetGitHubOutputs`, called only from `main.go` | no |
| The stderr diagnostic and its exact strings (F-17..F-19, AC-4, AC-5, AC-6) | `main.go` | no |

The spec calls this out itself: F-30 states that orchestration written inline in `main.go`
"would ship untested", and §11 requires the decision to be made "before coding, not after".

The plan's per-phase E2E requirement makes this sharper: the central acceptance criterion of
the whole feature (AC-1, "exits 1 with the broken overlay in `failed-count`") is a statement
about a **process exit code**, and no in-process harness can assert one honestly.

## Decision

**Add a second entry point to the integration harness that builds and executes the real
`cmd/action` binary, and leave the existing in-process `run()` untouched.**

```go
// built once per package run, in TestMain
var actionBin string   // path to a binary produced by `go build ./cmd/action`

// runResult is what an out-of-process run of the action yields.
type runResult struct {
    ExitCode int
    Stdout   string
    Stderr   string
    Outputs  map[string]string // parsed from the temp $GITHUB_OUTPUT file
    Summary  string            // contents of the temp $GITHUB_STEP_SUMMARY file
}

// runBinary executes cmd/action against this repo fixture with the given INPUT_* env.
func (r *repo) runBinary(env map[string]string) runResult
```

Concretely:

- `TestMain` runs `go build -o <tmpdir>/kustomize-build-check ./cmd/action` once. If `go` is
  not on `PATH` the package skips with a message naming the reason, using the same
  `requireBinary` pattern as `git` and `kustomize` (`pipeline_test.go:142-148`). In CI this
  cannot silently skip: the tests are themselves invoked by `go test`, so `go` is present.
- `runBinary` sets `cmd.Dir` to the fixture repo (the cwd invariant of
  change-detection.spec.md §5), passes `INPUT_BASE-REF`, `INPUT_ROOT-DIR`,
  `INPUT_FAIL-ON-ERROR`, `INPUT_ENABLE-HELM` and `INPUT_ON-UNRESOLVABLE-BASE` explicitly,
  and points `GITHUB_OUTPUT` and `GITHUB_STEP_SUMMARY` at fresh temp files.
- The exit code is read from `*exec.ExitError`; exit 0 yields `ExitCode: 0`.
- `Outputs` is parsed by splitting each line of the `GITHUB_OUTPUT` file on the first `=`,
  so a criterion can assert `res.Outputs["change-detection-mode"] == "full-scan"` directly.
- The existing `run()` helper, its `reporter.Summary` return value, and all seven tests that
  use it are **not** modified. The 31-test baseline is preserved by construction.

## Alternatives Considered

| Option | Pros | Cons |
|--------|------|------|
| **Build and execute the binary from `internal/integration` (chosen)** | Asserts the real exit code, the real `INPUT_*` parsing and the real `$GITHUB_OUTPUT` file; no drift is possible because there is nothing to keep in sync; every later plan (build-timeout, container-hardening) inherits it | Requires `go` on `PATH` and a one-off build (~1s); test failures report through stdout/stderr strings rather than Go values |
| Mirror the new orchestration inside `run()` | Smallest diff; no new machinery | This is exactly the drift F-30 warns about: the harness could pass while `main.go` is wrong, which is the failure mode the spec asks the planner to design away. Still cannot assert an exit code. Disqualifying. |
| Extract `func run(...) int` in package `main`, test in `cmd/action/main_test.go` | Testable without a subprocess; fast | `internal/integration` cannot import package `main`, so the repo's E2E layer still would not cover it, and the plan requires an E2E row per phase. Also leaves `os.Exit` and env parsing untested. |
| Re-exec the test binary as the CLI (`TestMain` + a sentinel env var calling `main()`) | No `go build`, no `go` on PATH | Only works if the test lives in `cmd/action`, which has the same reachability problem as the option above |
| Inject a `func(int)` exit hook into `main` | No subprocess | Tests a mock of the thing under test; `$GITHUB_OUTPUT` and stderr wiring remain uncovered |
| Run the container image (`docker run`) | Closest to production | Requires a Docker daemon in the test environment; turns a seconds-long unit-test loop into a build-and-run loop. Reserved for the container-hardening work. |

## Consequences

**Positive**

- Exit-code semantics (§6.4) become executable. The one genuinely behaviour-breaking element
  of this feature — exit 1 → 0 on the unresolvable-base path — is guarded by a test that
  reads the actual exit code.
- `INPUT_*` parsing, including the safe-default rule for an unrecognised
  `on-unresolvable-base` (F-15, AC-8), is covered without a second implementation.
- `internal/git`'s `L-01` (no unit tests) and `main.go`'s complete absence of coverage are
  both reduced by the same change.
- Later plans get it for free: build-timeout-handling needs the same ability to assert
  `INPUT_BUILD-TIMEOUT` parsing and a timed-out result's rendering.

**Negative / accepted**

- The package now depends on `go` at test time, in addition to `git` and `kustomize`.
  CLAUDE.md's "don't let the integration tests silently skip in CI" applies: the skip message
  must name `go` explicitly so a skipped run is visible in the log.
- Two harness entry points coexist. `run()` is for affected-set and skip-classification
  assertions; `runBinary` is for exit codes, outputs and diagnostics. The plan states which
  to use per criterion so the split does not become folklore.
- A build failure in `cmd/action` now surfaces as a `TestMain` failure rather than a compile
  error in the package under test. Acceptable — it is still a hard failure.
- Slower: one `go build` per package run plus one process spawn per scenario, against an
  existing per-scenario cost that already includes real `git` and real `kustomize`.
