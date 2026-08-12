package analyzer

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/michielvha/kustomize-build-check/internal/discovery"
	"github.com/michielvha/kustomize-build-check/internal/graph"
)

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
	files := []discovery.KustomizeFile{{
		Path:      filepath.Join(baseDir, "kustomization.yaml"),
		Dir:       baseDir,
		Resources: []string{"deployment.yaml"},
	}}

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
	files := []discovery.KustomizeFile{{
		Path:      filepath.Join(baseDir, "kustomization.yaml"),
		Dir:       baseDir,
		Resources: []string{"deployment.yaml"},
	}}

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
	files := []discovery.KustomizeFile{{
		Path:      filepath.Join(otherDir, "kustomization.yaml"),
		Dir:       otherDir,
		Resources: []string{"deployment.yaml"},
	}}

	affected := New().GetAffectedKustomizations(
		[]string{"base/deployment.yaml"}, newTestGraph(t, files), files)

	if len(affected) != 0 {
		t.Errorf("expected no kustomizations affected, got %v", affected)
	}
}
