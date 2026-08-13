package analyzer

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/michielvha/kustomize-build-check/internal/discovery"
	"github.com/michielvha/kustomize-build-check/internal/graph"
)

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

	if !slices.Contains(affected, baseDir) {
		t.Errorf("expected %s to be affected, got %v", baseDir, affected)
	}
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

	if slices.Contains(affected, baseDir) {
		t.Errorf("a change in base-old must not affect base, got %v", affected)
	}
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
	if !slices.Contains(affected, want) {
		t.Errorf("expected %s to reach the build step, got %v", want, affected)
	}
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

	if len(affected) != 0 {
		t.Errorf("expected no kustomizations affected, got %v", affected)
	}
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

			got := slices.Contains(affected, baseDir)
			if got != tt.want {
				t.Errorf("changed %q: affected=%v, want %v (affected set: %v)",
					tt.changedFile, got, tt.want, affected)
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

	if !slices.Contains(affected, baseDir) {
		t.Errorf("a cross-directory resource reference must mark its kustomization affected; got %v", affected)
	}
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

	if !slices.Contains(affected, kustDir) {
		t.Errorf("a reference to an ancestor must match files beneath it; got %v", affected)
	}
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

	if len(affected) != 0 {
		t.Errorf("an unreferenced file must affect nothing; got %v", affected)
	}
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
