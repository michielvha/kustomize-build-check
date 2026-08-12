---
description: "Classify a resources entry as a directory reference by asking whether a kustomization was discovered there, not by stat and not by filename extension"
status: proposed
date: 2026-08-12
author: "SDD Planner (automated), run by michielvha <michielvh@outlook.com>"
---

# ADR-001: Graph reference classification (resolves OQ-2)

## Status

proposed — resolves OQ-2 of
[complete-impact-matching.spec.md](../docs/specs/complete-impact-matching.spec.md) §10,
which blocks Phase 2 of [plans/complete-impact-matching.md](../plans/complete-impact-matching.md).

## Context

`graph.extractDependencies` decides which `resources` entries are directory references
(and therefore candidates for a base→overlay edge) with a filename heuristic:

```go
// graph.go:100-108
for _, resource := range file.Resources {
    if filepath.Ext(resource) != "" {   // "skip if it's a file"
        continue
    }
    deps = append(deps, resource)
}
```

`filepath.Ext("../bases/v1.2")` is `".2"` and `filepath.Ext("../my.app")` is `".app"`, so a
directory whose name contains a dot is misclassified as a file and silently loses every
edge to it. Changing that base then validates nothing downstream — false-pass class **G4**.

F-B1/F-B2 require classification by what the path *is*. OQ-2 tabled three mechanisms and
left the answer to the repo owner, with the deleted-base case named as the deciding
scenario, because a reference to a base whose kustomization file the change deleted must
still let the removed path reach the builder and be reported **skipped**, never **failed**.

The load-bearing fact is what the classification is *for*. `deps` has exactly one consumer
that changes behaviour, `graph.go:68-85`:

```go
for _, dep := range deps {
    absDepPath := filepath.Clean(filepath.Join(file.Dir, dep))
    if depNode, exists := g.nodes[absDepPath]; exists {   // <- the real classifier
        depNode.IsBase = true
        g.reverseLookup[absDepPath] = append(g.reverseLookup[absDepPath], file.Dir)
    } else {
        slog.Debug("Dependency not found in discovered kustomizations", ...)
    }
}
```

An entry only ever becomes an edge if a **discovered kustomization** sits at the resolved
path. The extension test is therefore not a classifier at all, it is a pre-filter in front
of a classifier that already exists and is already exact. Its only observable effect is to
drop true positives. The other consumer, `Node.Dependencies`, is read solely by
`DependencyGraph.String()` (`graph.go:211-216`) — a debug rendering with no in-repo caller.

## Decision

**Adopt OQ-2 option (ii), implemented as a deletion rather than a replacement.** Remove
the `filepath.Ext` pre-filter and pass every `resources` entry through to the existing
discovered-node lookup, which becomes the sole and only classifier:

```go
func (g *DependencyGraph) extractDependencies(file *discovery.KustomizeFile) []string {
    deps := make([]string, 0, len(file.Resources)+len(file.Bases)+len(file.Components))
    deps = append(deps, file.Resources...)
    deps = append(deps, file.Bases...)
    deps = append(deps, file.Components...)
    return deps
}
```

Consequences that follow directly:

- **A path that does not exist produces no node, hence no edge, hence no panic and no error**
  (F-B4, AC-B5). The behaviour is deterministic and needs no missing-path policy, because
  the graph never touches the filesystem — it only asks a map it already built.
- **No filesystem I/O is added anywhere** (NF-02, NF-03).
- **A file entry can never create a spurious edge.** `deployment.yaml` resolves to
  `<dir>/deployment.yaml`, which is a node only if a directory of that name holds a
  kustomization file — in which case it genuinely *is* a directory reference and the edge
  is correct.
- `Node.Dependencies` changes meaning from "directory candidates" to "raw references as
  written". The field name is kept (NF-08); its doc comment is updated.

### The deleted-base case, concretely

OQ-2's stated weakness of option (ii) is that it "misses a directory reference to a
directory whose kustomization file the change deleted". Three findings, all probed this
session against the real pipeline, show the weakness is not real in the outcomes that matter:

1. **Options (i) and (iii) cannot fix it either.** A stat on a deleted base returns
   `ENOENT`. Even if the missing path were then classified as a directory, the edge still
   requires `g.nodes[absDepPath]` to exist, and discovery walks the *post-change* tree, so
   there is no node. The stat changes the value of `deps` and nothing downstream of it.
   Options (i) and (iii) buy filesystem I/O for a provably identical result.

2. **The skip path does not run through the graph at all.** A changed
   `kustomization.yaml` is matched by basename and its directory is added unconditionally
   (`analyzer.go:51-58`), then `builder.skipReason` (`builder.go:127-143`) records the skip.
   No edge is consulted. Probed with Phase 1 + Phase 2 applied together: deleting `base/`
   outright yielded `base` → `Skipped, reason "removed in this change"`. The three guards
   `TestDeletedDirectoryIsSkipped`, `TestConsolidateDuplicatedDirsIntoComponent` and
   `TestRenamedDirectoryValidatesNewPath` all pass unmodified.

3. **Phase 1 covers the propagation that option (ii) cannot.** With the containment guard
   removed, the deleted base's *other* files (`base/deployment.yaml`) are still in the diff
   and now match the overlay's resolved `../../base` reference, so the dependent overlay is
   marked affected through the analyzer instead of through the graph. Probed: on `main` the
   scenario reports `failed=0, skipped=1`, **exit 0** — a fifth, previously unrecorded false
   pass. With Phase 1 applied it reports `overlays/dev` → **failed** and `base` → skipped,
   which is the truthful outcome.

## Alternatives Considered

| Option | Pros | Cons |
|--------|------|------|
| **(i) `os.Stat` the resolved path** | Reflects what is actually on disk; intuitively "what the path is" | Adds I/O to graph construction (NF-03 pressure); needs an invented missing-path policy; **cannot change any outcome**, because the edge still requires a discovered node. Pure cost. |
| **(ii) Is a kustomization discovered at the resolved path? (chosen)** | Zero I/O; exactly the question an edge encodes; deterministic for missing paths by construction; net **deletion** of code, satisfying `CLAUDE.md` "is it needed at all?" | Cannot distinguish "directory reference to a deleted base" from "file reference"; mitigated by Phase 1 (finding 3) and irrelevant to skip semantics (finding 2). |
| **(iii) Lookup first, stat as fallback** | Superset of (ii) | All of (i)'s cost for none of its benefit; the fallback branch is unreachable in terms of observable behaviour. Two mechanisms to keep correct instead of one. |
| Keep `filepath.Ext`, special-case known extensions | Smallest diff | Unfixable: `.2` and `.app` are indistinguishable from real extensions without knowing the filesystem. Leaves G4 open. |

## Consequences

**Positive**

- G4 closes for every dotted directory name, with no allow-list to maintain.
- `extractDependencies` loses its only branch and becomes three appends.
- No new dependency, no new I/O, no new failure mode (F-E7, NF-02, NF-03).

**Negative / accepted**

- `TestExtractDependencies` (`graph_test.go:130`) asserts 3 candidates from
  3 resources + 1 base + 1 component and will report 5. Probed: this is the **only**
  failing test across the whole suite under this change. It is a count assertion on an
  intermediate value, not on behaviour, so it is updated to 5 and paired with a new test
  that asserts the *edges* produced — the property it was standing in for. Rationale
  recorded here per AC-B4.
- `graph.String()` debug output now lists file references under "Dependencies". Cosmetic,
  no caller.
- Residual gap, recorded rather than fixed: a base deleted so completely that its
  directory held nothing but `kustomization.yaml` still propagates nothing through the
  graph. Closing it would require building the graph from the pre-change tree, which is
  out of scope for this spec. Tracked as OQ-6 in the plan.
