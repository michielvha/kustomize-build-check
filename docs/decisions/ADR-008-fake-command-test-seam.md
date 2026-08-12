---
description: "Inject the build command as an unexported package field and re-exec the test binary through a TestMain env gate, because Build owns its own argv and cannot be handed a -test.run flag."
status: proposed
date: 2026-08-12
author: "SDD Planner (automated), run by michielvha <michielvh@outlook.com>"
---

# ADR-008: The fake-command seam for timeout tests

## Status

Proposed. Accepted when Phase 1 of [build-timeout-handling](../plans/build-timeout-handling.md)
lands.

## Context

`build-timeout-handling.spec.md` F-35 requires the timeout tests to be hermetic: no dependency on
a real slow `kustomize`, no dependency on `kustomize` being installed, well under a second each
(AC-11). F-34 proposes the seam: an unexported command-name field defaulting to `"kustomize"`,
usable because `builder_test.go` is `package builder` (`builder_test.go:1`) and can construct the
struct literal directly.

The spec then suggests "the stdlib `os/exec` test idiom: re-exec the test binary itself
(`os.Args[0]` plus a marker env var and a `TestHelperProcess`-style helper)". That idiom, as
written in the stdlib, passes `-test.run=TestHelperProcess --` as the **first arguments** to the
re-exec'd binary.

**`Build` cannot do that.** It builds its own argv unconditionally
(`builder.go:67-71`): `[]string{"build"}`, optionally `--enable-helm`, then `path`. A caller has
no way to prepend flags. So a bare `os.Args[0]` re-exec would invoke the test binary as
`testbin build /some/path` — with no `-test.run` filter, which means the child runs the **entire
test suite again**, recursively, once per fake build.

This ADR fixes how the fake command is selected and how the child is prevented from recursing.

## Decision

Two parts.

1. **The seam stays a plain unexported `command string` field on `builder`, defaulting to
   `"kustomize"`.** No argv-prefix slice, no exported API, no interface for `exec`. Tests
   construct `&builder{timeout: d, command: os.Args[0], grace: g}` directly.

2. **The child is gated in `TestMain`, not by `-test.run`.** `internal/builder` gains a `TestMain`
   that inspects a marker environment variable and, when set, runs the fake-command behaviour and
   returns **without ever calling `m.Run()`**:

   ```go
   func TestMain(m *testing.M) {
       if mode := os.Getenv("KBC_BUILDER_FAKE_MODE"); mode != "" {
           fakeKustomize(mode) // writes output, then exits
           return
       }
       os.Exit(m.Run())
   }
   ```

   The test sets the marker with `t.Setenv`, so the child inherits it (`cmd.Env` is nil, so
   `exec` passes `os.Environ()`), and the parent's own `TestMain` already ran before the variable
   existed. `fakeKustomize` switches on the mode: hang, hang-with-descendant, fail fast, or
   succeed.

**Verified this session** with a standalone probe reproducing `Build`'s argv construction: three
tests (hang → `TimedOut`, fail-fast → ordinary failure, success → success) passed in **0.66 s
total**, the positional args `[build <path>]` reached the helper intact, no recursion occurred,
and partial stdout written before the kill was still captured on the timed-out result — which is
what makes F-13 testable.

## Alternatives Considered

| Option | Pros | Cons |
|---|---|---|
| **TestMain env gate + `command string` (chosen)** | Hermetic; no external binary; smallest seam (one string field); works with `Build` owning its argv; measured 0.66 s for three cases | `TestMain` is package-global, so every future test in `internal/builder` inherits the gate; the marker name becomes a package-internal convention |
| Stdlib `TestHelperProcess` + `-test.run` | The canonical, most recognisable idiom | **Does not work here.** Requires prepending flags to argv, which `Build` does not allow. Would force the seam to widen from `string` to `[]string` (an argv prefix) purely to satisfy the test, making the production field carry a shape no production caller uses |
| Seam as `[]string` argv prefix | Would enable the stdlib idiom verbatim | Wider seam for no production benefit; a production `command` is one binary, and a slice invites arbitrary flag injection into a security-relevant `exec` call |
| System `sleep` / `sh` as the fake command | Zero new test scaffolding | F-35 allows it only as a fallback, and it must skip cleanly when absent (`pipeline_test.go:142-148`). A skipping test is not coverage; the timeout path currently has **none** (B-10), so the one thing this change must not ship is a test that quietly does not run. Also cannot simulate "fails fast with stderr" and "hangs" from one mechanism |
| Abstract `exec` behind an interface and mock it | Fully hermetic; no subprocess at all | Mocks out the exact thing under test. The bug class here (B-8 grandchild pipes, B-9 nil `Process`) lives **in real process lifecycle**; a mock would have passed against the buggy code. Rejected on the constitution's correctness bar |

## Consequences

- `internal/builder` gains a `TestMain`. Any future test in that package must not set the marker
  variable for unrelated reasons.
- The three existing skip tests (`builder_test.go:11-63`) are unaffected: they never reach the
  exec path, and `New()` keeps its signature and its 2-minute default (F-33).
- `internal/integration` is a **different package** and therefore cannot use this seam at all. Its
  E2E timeout coverage uses `builder.NewWithTimeout` with a sub-millisecond limit against the real
  `kustomize`, which times out deterministically because no real build completes in 1 ms. See the
  plan's Test Plan.
- The marker variable is an internal test detail and is never read by production code.
