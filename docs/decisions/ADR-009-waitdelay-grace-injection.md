---
description: "Keep the 5-second WaitDelay grace as the production default but hold it in an unexported field rather than a const, because a hard const makes the AC-10 wall-clock test cost 5.3s and breach AC-11."
status: accepted
date: 2026-08-12
author: "SDD Planner (automated), run by michielvha <michielvh@outlook.com>"
---

# ADR-009: Where the `WaitDelay` grace lives

## Status

Proposed. Accepted when Phase 1 of [build-timeout-handling](../plans/build-timeout-handling.md)
lands. **Requires a wording amendment to `build-timeout-handling.spec.md` F-10** (see
Consequences).

## Context

Spec F-10 requires `cmd.WaitDelay` to be set so the per-build limit is a real wall-clock bound
when a grandchild inherits the output pipe (B-8), and specifies: *"The grace period MUST be a
constant in `internal/builder`, **5 seconds**, and MUST NOT be user-configurable."*

Spec F-36(d) requires a test that the bound holds — *"a fake command that leaves a descendant
holding the pipe still returns within `limit + grace + slack`"* — and calls it "the only guard on
the difference between a documented bound and a real one" (§11 risk 4).

Spec AC-11 requires `go test ./internal/builder` to add **under 2 seconds** of runtime.

These three cannot all hold if the grace is a Go `const`. Measured this session, a child that
spawns a descendant outliving it, against a 300 ms deadline:

| `WaitDelay` | `Run()` returned after |
|---|---|
| unset (today's behaviour) | **30.0 s** (when the descendant exited) |
| 5 s | **5.3 s** |
| 500 ms | **0.80 s** |

A `const` grace of 5 s makes the single most important test in the change cost 5.3 s on its own,
about 8× the entire current `internal/builder` suite, and breaches AC-11 by a factor of three.
The likely reaction to a slow test is to delete it, which returns the codebase to "documented
bound, unverified" — the exact state B-8 describes.

## Decision

The grace stays **5 seconds in production and stays out of the public surface**, but it is held in
an **unexported field on the `builder` struct**, defaulted from an unexported package constant:

```go
const defaultWaitGrace = 5 * time.Second

type builder struct {
    timeout time.Duration
    grace   time.Duration // defaults to defaultWaitGrace; injectable within package builder only
    command string        // ADR-008
}
```

`New()` and `NewWithTimeout(d)` both set `grace: defaultWaitGrace`. Nothing else assigns it. The
F-36(d) test constructs `&builder{timeout: 300ms, grace: 200ms, command: os.Args[0]}` and asserts
the return is within `limit + grace + slack` — measured at ~0.8 s, comfortably inside AC-11.

"MUST NOT be user-configurable" is honoured in full and in the sense that matters: there is **no
action input**, **no environment variable**, **no exported constructor parameter** and **no
exported field**. The only writer outside the constructors is a test in the same package. This is
the identical treatment F-34 already prescribes for the command name (ADR-008), so it adds no new
kind of seam.

## Alternatives Considered

| Option | Pros | Cons |
|---|---|---|
| **Unexported field, `defaultWaitGrace = 5s` (chosen)** | Production bound unchanged at `limit + 5s`; the F-36(d) test costs ~0.8 s; same seam shape as ADR-008; no public surface | Deviates from F-10's literal word "constant"; needs a spec amendment |
| Hard `const 5s`, keep the test | Literal spec compliance | Test costs 5.3 s, breaches AC-11 3×, and is the test most likely to be deleted later for slowness |
| Hard `const 5s`, drop the F-36(d) test | Literal compliance, fast suite | Removes the **only** guard on the real-vs-documented bound (§11 risk 4). `WaitDelay` is easy to omit and its absence is invisible until a `--enable-helm` build hangs in production. Unacceptable |
| Shorten the const to ~500 ms so the test is fast | One number, no seam | Trades a production property for test convenience. 500 ms may be too short for a healthy process to flush a large manifest stream, risking truncated `Output`/`Error` on ordinary builds — a diagnostic regression on the **non**-timeout path, which is every build |
| Make it an action input | Fully configurable | Explicitly forbidden by F-10, and it is a knob with no user-meaningful semantics |

## Consequences

- **Spec amendment required.** `build-timeout-handling.spec.md` F-10 should be reworded from
  "MUST be a constant in `internal/builder`" to "MUST default to 5 seconds from an unexported
  package constant and MUST NOT be exposed as an input, an environment variable or any exported
  API". The 5-second value, the non-configurability and the requirement itself are unchanged.
  Recorded as an amendment in the plan's Open Questions, to be applied with the other §11
  amendments after implementation.
- Production behaviour is exactly what F-10 asked for: worst-case per build is `limit + 5s`, and
  worst case per run stays `n × (limit + 5s)` (F-11).
- OQ-3 ("is 5 s the right grace?") becomes cheaper to answer: the value is now a single default
  with a test that pins the *mechanism* independently of the number.
- The grandchild is still not killed (OQ-4 unchanged). This ADR bounds the wait; it does not reap
  the orphan.
