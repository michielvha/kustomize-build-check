package analyzer

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/michielvha/kustomize-build-check/internal/discovery"
	"github.com/michielvha/kustomize-build-check/internal/graph"
)

// assertAffectedSet compares the affected set for exact equality, so
// over-matching fails as loudly as under-matching (F-E4). Containment checks
// cannot see a spurious extra entry, which is the failure mode that matters when
// a guard has just been deleted.
func assertAffectedSet(t *testing.T, got []string, want ...string) {
	t.Helper()

	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if !slices.Equal(g, w) {
		t.Errorf("affected set mismatch\n  got:  %v\n  want: %v", g, w)
	}
}

// kustFile builds a KustomizeFile the way discovery does, so fixtures cannot
// drift from production. FileRefs is what the analyzer matches against; a
// fixture setting only Resources would be an object discovery never emits.
func kustFile(dir string, resources ...string) discovery.KustomizeFile {
	refs := make([]discovery.Ref, 0, len(resources))
	for _, r := range resources {
		refs = append(refs, discovery.Ref{Field: "resources", Raw: r})
	}
	return discovery.KustomizeFile{
		Path:      filepath.Join(dir, "kustomization.yaml"),
		Dir:       dir,
		Resources: resources,
		FileRefs:  refs,
	}
}

// newTestGraph builds a graph over the given kustomizations.
func newTestGraph(t *testing.T, files []discovery.KustomizeFile) graph.Graph {
	t.Helper()

	g := graph.New()
	if err := g.Build(files); err != nil {
		t.Fatalf("graph Build failed: %v", err)
	}
	return g
}

// TestChangedResourceFileMarksKustomizationAffected covers the path
// normalization: git reports repo-relative paths while discovery records
// absolute directories. Before they were normalized, a changed resource file
// matched nothing and the run reported success without validating anything.
func TestChangedResourceFileMarksKustomizationAffected(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	baseDir := filepath.Join(root, "base")
	files := []discovery.KustomizeFile{kustFile(baseDir, "deployment.yaml")}

	// Relative, exactly as `git diff --name-only` emits it.
	affected := New().GetAffectedKustomizations(
		[]string{"base/deployment.yaml"}, newTestGraph(t, files), files)

	assertAffectedSet(t, affected, baseDir)
}

// TestSiblingDirectoryIsNotMatchedByPrefix guards the separator-sensitive
// prefix check: "base-old" must not be treated as living inside "base".
func TestSiblingDirectoryIsNotMatchedByPrefix(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	baseDir := filepath.Join(root, "base")
	files := []discovery.KustomizeFile{kustFile(baseDir, "deployment.yaml")}

	affected := New().GetAffectedKustomizations(
		[]string{"base-old/deployment.yaml"}, newTestGraph(t, files), files)

	assertAffectedSet(t, affected)
}

// TestDeletedKustomizationStillReachesBuildStep documents the division of
// responsibility: the analyzer does not filter removed paths, because it cannot
// distinguish "deleted" from "moved". The build step drops paths that no longer
// exist, which is where the skip is recorded and reported.
func TestDeletedKustomizationStillReachesBuildStep(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	// Nothing was discovered on disk: the directory is gone in the head commit.
	var files []discovery.KustomizeFile

	affected := New().GetAffectedKustomizations(
		[]string{"overlays/obsolete/kustomization.yaml"}, newTestGraph(t, files), files)

	want := filepath.Join(root, "overlays", "obsolete")
	assertAffectedSet(t, affected, want)
}

// TestChangedFileUnderUnrelatedKustomizationIsIgnored keeps the analyzer honest:
// only kustomizations that actually reference the file are affected.
func TestChangedFileUnderUnrelatedKustomizationIsIgnored(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	otherDir := filepath.Join(root, "other")
	files := []discovery.KustomizeFile{kustFile(otherDir, "deployment.yaml")}

	affected := New().GetAffectedKustomizations(
		[]string{"base/deployment.yaml"}, newTestGraph(t, files), files)

	assertAffectedSet(t, affected)
}

// TestCrossDirectorySiblingPrefixIsNotMatched is the F-E2 regression guard for
// Phase 1 of the complete-impact-matching plan (AC-A4, AC-A5, AC-E3).
//
// Phase 1 deletes the containment guard that required a changed file to live
// under the kustomization's own directory. That guard was also, incidentally,
// the thing keeping a sibling directory whose name merely starts with a
// reference's name from matching. Once it is gone, the only protection left is
// the separator-terminated prefix test against the *resolved* reference path.
//
// This test pins that protection. It is written to pass both before and after
// the guard is removed: before, because the guard rejects everything; after,
// because the resolved-path comparison is precise on its own. What it must
// never do is start matching `shared-old` or `sharedx`.
func TestCrossDirectorySiblingPrefixIsNotMatched(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	// A kustomization at <root>/base that reaches OUT of its own directory.
	baseDir := filepath.Join(root, "base")
	files := []discovery.KustomizeFile{kustFile(baseDir, "../shared")}

	tests := []struct {
		name        string
		changedFile string
		want        bool
	}{
		{"sibling with shared prefix", "shared-old/x.yaml", false},
		{"sibling with no separator", "sharedx/y.yaml", false},
		{"nested inside the reference", "shared/nested/deep.yaml", true},
		{"the reference directory itself", "shared", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			affected := New().GetAffectedKustomizations(
				[]string{tt.changedFile}, newTestGraph(t, files), files)

			if tt.want {
				assertAffectedSet(t, affected, baseDir)
			} else {
				assertAffectedSet(t, affected)
			}
		})
	}
}

// TestCrossDirectoryReferenceMarksKustomizationAffected is the direct G1 guard:
// a kustomization that reaches outside its own directory must be validated when
// the file it reaches for changes (F-A1, F-A3).
func TestCrossDirectoryReferenceMarksKustomizationAffected(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	baseDir := filepath.Join(root, "base")
	files := []discovery.KustomizeFile{kustFile(baseDir, "../shared/cm.yaml")}

	affected := New().GetAffectedKustomizations(
		[]string{"shared/cm.yaml"}, newTestGraph(t, files), files)

	assertAffectedSet(t, affected, baseDir)
}

// TestAncestorReferenceMatchesEverythingBelowIt documents a consequence of
// matching on the resolved path: a reference to an ancestor directory matches
// every file beneath it. That is over-broad but truthful — the kustomization
// really does pull in that whole subtree — and it errs on the false-fail side,
// which is the correct side for this tool.
func TestAncestorReferenceMatchesEverythingBelowIt(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	kustDir := filepath.Join(root, "apps", "web")
	// ".." resolves to <root>/apps
	files := []discovery.KustomizeFile{kustFile(kustDir, "..")}

	affected := New().GetAffectedKustomizations(
		[]string{"apps/other/deployment.yaml"}, newTestGraph(t, files), files)

	// A reference to an ancestor is maximally over-broad, so assert the set
	// exactly: it must be this directory and nothing else.
	assertAffectedSet(t, affected, kustDir)
}

// TestUnrelatedCrossDirectoryFileIsNotMatched keeps the widened matching honest:
// removing the containment guard must not make everything match everything.
func TestUnrelatedCrossDirectoryFileIsNotMatched(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	baseDir := filepath.Join(root, "base")
	files := []discovery.KustomizeFile{kustFile(baseDir, "../shared/cm.yaml")}

	affected := New().GetAffectedKustomizations(
		[]string{"elsewhere/unrelated.yaml"}, newTestGraph(t, files), files)

	assertAffectedSet(t, affected)
}

// TestMatchReportsTheReferenceThatCausedIt covers F-A5/AC-A8: an unexpected
// match must be diagnosable from the reference that produced it, so the match
// carries both the raw and the resolved form.
func TestMatchReportsTheReferenceThatCausedIt(t *testing.T) {
	root := t.TempDir()
	kustDir := filepath.Join(root, "base")

	match, ok := (&analyzer{}).fileReferencedByKustomization(
		filepath.Join(root, "shared", "cm.yaml"),
		kustFile(kustDir, "../shared/cm.yaml"),
	)
	if !ok {
		t.Fatal("expected a match")
	}
	if match.Raw != "../shared/cm.yaml" {
		t.Errorf("raw reference = %q, want %q", match.Raw, "../shared/cm.yaml")
	}
	if match.Field != "resources" {
		t.Errorf("field = %q, want %q", match.Field, "resources")
	}
	if want := filepath.Join(root, "shared", "cm.yaml"); match.Resolved != want {
		t.Errorf("resolved reference = %q, want %q", match.Resolved, want)
	}
}

// TestAffectedSetContract pins the analyzer's output contract (AC-E7, F-E6),
// which had no test: absolute, cleaned, de-duplicated, never nil, no error
// return. Phase 3 added the always-affected rule and Phase 4 swapped the
// matching source, and both touch this return path.
func TestAffectedSetContract(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	baseDir := filepath.Join(root, "base")
	files := []discovery.KustomizeFile{
		// Two routes to the same directory: a direct reference and a ".."-containing
		// one that cleans to the same path. The result must contain it once.
		kustFile(baseDir, "cm.yaml", "./nested/../cm.yaml"),
	}

	affected := New().GetAffectedKustomizations(
		[]string{"base/cm.yaml"}, newTestGraph(t, files), files)

	if affected == nil {
		t.Fatal("the affected set must never be nil, even when empty")
	}
	seen := map[string]int{}
	for _, p := range affected {
		if !filepath.IsAbs(p) {
			t.Errorf("path %q must be absolute", p)
		}
		if p != filepath.Clean(p) {
			t.Errorf("path %q must be cleaned", p)
		}
		seen[p]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("path %q appears %d times; the set must be de-duplicated", p, n)
		}
	}

	// And the empty case is a non-nil empty slice, not nil.
	empty := New().GetAffectedKustomizations(nil, newTestGraph(t, nil), nil)
	if empty == nil {
		t.Error("the empty affected set must be a non-nil empty slice")
	}
}

// TestMatchLogsResolvedReference covers AC-A8/F-A5: an unexpected match must be
// diagnosable from the debug log alone, so the log carries the field, the raw
// reference and the resolved path.
func TestMatchLogsResolvedReference(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	baseDir := filepath.Join(root, "base")
	files := []discovery.KustomizeFile{kustFile(baseDir, "../shared/cm.yaml")}

	New().GetAffectedKustomizations(
		[]string{"shared/cm.yaml"}, newTestGraph(t, files), files)

	logged := buf.String()
	for _, want := range []string{"reference", "../shared/cm.yaml", "resolved", "field", "resources"} {
		if !strings.Contains(logged, want) {
			t.Errorf("debug log must carry %q so a match is diagnosable; got:\n%s", want, logged)
		}
	}
}
