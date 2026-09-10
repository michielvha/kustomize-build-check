# Spec Index

Specs are the source of truth for behaviour in this repository. Changes start here, not in
the code. See [CLAUDE.md](../../CLAUDE.md) for the constitution.

The five specs in the pipeline table below are **retro-specs**: they document behaviour that was already shipped
and already covered by tests, with a `file:line` citation behind every claim. They supersede
[design.md](../../design.md), which has drifted from the code in several places (recorded per
spec).

The pipeline runs in this order, and the specs are listed to match:

| # | Spec | Package | Description |
|---|------|---------|-------------|
| 1 | [change-detection](./change-detection.spec.md) | `internal/git` | Producing the list of files changed between `base-ref` and `HEAD`, and why deletions are deliberately retained. |
| 2 | [kustomization-discovery](./kustomization-discovery.spec.md) | `internal/discovery`, `internal/graph` | Finding every kustomization file, parsing its reference fields, and building the graph that propagates a base change to its overlays. |
| 3 | [impact-analysis](./impact-analysis.spec.md) | `internal/analyzer` | Combining the changed files, the discovered kustomizations and the graph into the set of directories that must be validated. |
| 4 | [build-execution](./build-execution.spec.md) | `internal/builder` | Running `kustomize build` per directory, and the skip guard that keeps a directory removed by the change from being reported as a failure. |
| 5 | [result-reporting](./result-reporting.spec.md) | `internal/reporter`, `cmd/action` | Console report, GitHub Actions outputs, job step summary, and the exit code that makes the check red or green. |

## The bar these specs encode

From [CLAUDE.md](../../CLAUDE.md): this tool is a CI gate, so its failure modes are
asymmetric.

- **A false pass is worse than a false fail.** Reporting green on a repo where
  `kustomize build` would fail defeats the tool's only purpose.
- **A false fail trains people to ignore the check**, so it is not cheap either.

Every spec names the false-pass surfaces in its own stage. When an ambiguity arises, that bar
decides which way it falls.

## Active work

Everything known to be coming is spec'd before implementation starts, so the whole surface is
visible up front rather than discovered mid-build.

| Spec | Status | Plan | Description |
|------|--------|------|-------------|
| [complete-impact-matching](./complete-impact-matching.spec.md) | **Shipped** (0.6.0) | [plan](../plans/complete-impact-matching.md) | Closes every false-pass surface in impact matching: cross-directory resource references, the unparsed reference fields (`patches`, `configMapGenerator`, `secretGenerator`, `helmCharts` and friends), unparseable kustomizations being silently dropped, and the `filepath.Ext` heuristic losing graph edges. Delivered in four phases. |
| [shallow-clone-support](./shallow-clone-support.spec.md) | **Shipped** | [plan](../plans/shallow-clone-support.md) | The action hard-fails with a raw `fatal: bad object` when the base ref is not reachable locally, which is what `actions/checkout`'s default `fetch-depth: 1` produces. Detects the case, explains it, and degrades to validating everything rather than concluding "nothing changed". |
| [build-timeout-handling](./build-timeout-handling.spec.md) | **Shipped** | [plan](../plans/build-timeout-handling.md) | A timed-out build is currently indistinguishable from a broken kustomization, sending people to debug the wrong thing. Makes the cause machine-readable, adds a `build-timeout` input, and fixes a latent nil-pointer panic in the kill timer. |
| [container-hardening](./container-hardening.spec.md) | **Shipped** | [plan](../plans/container-hardening.md) | Moves the image off `alpine:3.23` to a Wolfi base to cut CVE surface, with behaviour parity as a hard requirement. Full distroless was considered and rejected: it needs go-git, costing 48 extra modules and a behaviour change. |
| [component-skip-semantics](./component-skip-semantics.spec.md) | **Draft** (2026-09-10) | not yet planned | A `kind: Component` is handed to `kustomize build` as if it were a standalone target, so any component carrying a patch fails the check while every overlay that consumes it builds green. Found live on `argocd-k8s-resources` PR #196, where the workaround was to abandon the component and duplicate the patch. Reports components as skipped with a distinct reason and relies on the `components:` graph edges that already validate them through their parents. |

**Sequencing.** `complete-impact-matching` first — it is the only one fixing active false passes.
Then `shallow-clone-support`, then `build-timeout-handling`, then `container-hardening` (no
behaviour change, so it is safest last, and it wants the smoke-test harness built against the
current image first). `component-skip-semantics` is independent of all four and can be taken
whenever; it is the only one fixing an active false *fail*.

One decision is outstanding, and it is deliberate:
[component-skip-semantics](./component-skip-semantics.spec.md) §10 O-1 leaves the choice between a
single `Skipped` counter and a per-reason split to the plan phase, because
`TestConsolidateDuplicatedDirsIntoComponent` (cited in
[build-execution](./build-execution.spec.md) §2 and AC-1) asserts against the single counter.
Everything else is spec'd well enough that implementation can start.

## Known gaps recorded by these specs

**The five false-pass rows below are CLOSED**, shipped in 0.6.0 by
[complete-impact-matching](./complete-impact-matching.spec.md). They are kept for the record, and
because each names the fixture that now guards it. Verified end to end against the released code:
every fixture where `kustomize build` fails exits non-zero, and every fixture where it succeeds
exits zero.

The remaining rows are still open and unclaimed.

| Gap | Spec | Effect |
|-----|------|--------|
| Cross-directory resource files are not matched | [impact-analysis](./impact-analysis.spec.md) | A kustomization referencing `../shared/cm.yaml` is not marked affected when that file changes. Verified: the run reports "No kustomizations affected". **False pass.** |
| Deleting a base leaves its overlays unvalidated | [complete-impact-matching](./complete-impact-matching.spec.md) §1 (G5) | Same root cause. The base is correctly skipped, nothing else is marked affected, and the run exits 0 while `kustomize build` on the surviving overlay fails. A regression introduced by the skip guard in #8, which previously caught this by accident. **False pass.** |
| `patches` / `configMapGenerator` / `secretGenerator` / `helmCharts` are not parsed | [kustomization-discovery](./kustomization-discovery.spec.md) | A file referenced only through those fields never marks anything affected. Verified: `helmCharts[].valuesFile` changes rendered output and its deletion breaks the build, so it is a real surface too. **False pass.** |
| Unparseable kustomization YAML is warned and skipped | [kustomization-discovery](./kustomization-discovery.spec.md) | It is excluded from the graph, so nothing depending on it is validated. **False pass.** |
| Dotted directory names lose graph edges | [kustomization-discovery](./kustomization-discovery.spec.md) | `filepath.Ext("../bases/v1.2")` is `".2"`, so the reference is treated as a file and the base→overlay edge is dropped. **False pass.** |
| A timed-out build is indistinguishable from a failed one | [build-timeout-handling](./build-timeout-handling.spec.md) | Reported identically apart from a WARN log line, so a slow build reads as a broken manifest. Now spec'd, along with a latent nil-pointer panic: the kill timer is armed before `cmd.Run()` starts the process, so a short timeout dereferences a nil `cmd.Process` in a goroutine with no recover. Unreachable at the current 2 minutes, hit immediately by any timeout test. |
| A kustomize Component is built as if it were a standalone target | [component-skip-semantics](./component-skip-semantics.spec.md) | A `kind: Component` carries no resources of its own and its patches resolve against the parent that lists it under `components:`, so a component holding a patch fails the check while all its consumers build green. Reproduced: `4 total, 3 successful, 1 failed, 0 skipped`, exit 1, on a correct change. **False fail**, and an expensive one: on PR #196 it forced the component to be abandoned and its patch duplicated into seven overlays. |
| `action.yml` documents a `base-ref` default the binary does not implement | [change-detection](./change-detection.spec.md) | Advertised as the PR base sha; the code implements `"" → HEAD~1`. Documentation defect. |
