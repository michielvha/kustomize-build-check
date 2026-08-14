# Image-level end-to-end harness

`go test` runs on the CI host, not inside the published image. It therefore
cannot see that a runtime binary is missing, has the wrong interpreter, or is not
executable by the runtime user. A base-image change can only cause a false pass
one way — the tool degrades, and the check still reports green — so that gap is
exactly where this harness sits.

| Script | What it proves |
|---|---|
| `make-fixture.sh` | Builds the fixture repository below. |
| `smoke.sh <image> [platform]` | Runs the real binary **in the image** against the fixture and asserts the observable counts and exit code. |
| `contract.sh <image>` | The image's runtime contract: effective UID, entrypoint shape, that `git`/`kustomize`/`helm` **execute** as the runtime user, and that no Go toolchain survives. |
| `assert-pinned.sh` | The base image is pinned by digest, not a floating tag. |

Run `smoke.sh <image> --capture` to regenerate `expected.env` rather than assert
against it.

## The plan-neutrality contract

`expected.env` holds the fixture's expected counts. **Changing those numbers means
the action's behaviour changed.** That is not necessarily wrong, but it must be
justified in the plan that causes it — and a base-image change must never move
them, because a base image cannot legitimately change what the tool validates.

The fixture is built so its counts stay stable across impact-matching changes:

| Fixture element | Why it is shaped this way |
|---|---|
| `apps/base` edited, `apps/overlay` references it | Exercises the ordinary path: a changed file marks its directory, and the graph propagates to the consuming overlay. |
| `apps/obsolete` deleted, and **nothing references it** | Exercises the skip path. A *referenced* deleted directory would flip from `failed=0, skipped=1` to `failed=1, skipped=1` when deleted-base handling changes — so referencing it would make this fixture non-neutral. |
| No cross-directory file references | Those are rejected by kustomize's default load restrictor, so they cannot be part of a fixture that must build. |
| No malformed YAML, no undecodable fields | Those trigger the always-affected rule, whose scope has changed more than once. |

## Why the self-test exists

`image-check.yml` also builds two deliberately broken images — one with `git`
removed, one with `kustomize` removed — and requires `smoke.sh` to **fail** against
both. A harness that passes against a broken image is worse than no harness,
because it reads as coverage.
