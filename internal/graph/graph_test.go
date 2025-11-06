package graph

import (
	"path/filepath"
	"testing"

	"github.com/michielvha/kustomize-build-check/internal/discovery"
)

func TestBuildGraph(t *testing.T) {
	files := []discovery.KustomizeFile{
		{
			Path:      "/test/base/kustomization.yaml",
			Dir:       "/test/base",
			Resources: []string{"deployment.yaml"},
		},
		{
			Path:      "/test/overlays/dev/kustomization.yaml",
			Dir:       "/test/overlays/dev",
			Resources: []string{"../../base", "patch.yaml"},
		},
		{
			Path:      "/test/overlays/prod/kustomization.yaml",
			Dir:       "/test/overlays/prod",
			Resources: []string{"../../base"},
		},
	}

	g := New()
	err := g.Build(files)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Test that base is marked as base
	if !g.IsBase("/test/base") {
		t.Error("expected /test/base to be marked as base")
	}

	// Test that overlays are not bases
	if g.IsBase("/test/overlays/dev") {
		t.Error("expected /test/overlays/dev to not be a base")
	}

	// Test dependent overlays
	overlays := g.GetDependentOverlays("/test/base")
	if len(overlays) != 2 {
		t.Errorf("expected 2 dependent overlays, got %d", len(overlays))
	}
}

func TestGetDependentOverlays(t *testing.T) {
	files := []discovery.KustomizeFile{
		{
			Dir:       "/test/base",
			Resources: []string{},
		},
		{
			Dir:       "/test/overlay1",
			Resources: []string{"../base"},
		},
		{
			Dir:       "/test/overlay2",
			Resources: []string{"../base"},
		},
		{
			Dir:       "/test/overlay3",
			Resources: []string{"deployment.yaml"},
		},
	}

	g := New()
	if err := g.Build(files); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	overlays := g.GetDependentOverlays("/test/base")

	if len(overlays) != 2 {
		t.Errorf("expected 2 overlays, got %d", len(overlays))
	}

	// Verify both overlays are in the list
	found1, found2 := false, false
	for _, overlay := range overlays {
		cleanOverlay := filepath.Clean(overlay)
		if cleanOverlay == "/test/overlay1" {
			found1 = true
		}
		if cleanOverlay == "/test/overlay2" {
			found2 = true
		}
	}

	if !found1 || !found2 {
		t.Error("expected to find both overlay1 and overlay2")
	}
}

func TestIsBase(t *testing.T) {
	files := []discovery.KustomizeFile{
		{
			Dir:       "/test/base",
			Resources: []string{},
		},
		{
			Dir:       "/test/overlay",
			Resources: []string{"../base"},
		},
	}

	g := New()
	if err := g.Build(files); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if !g.IsBase("/test/base") {
		t.Error("expected base to be identified as base")
	}

	if g.IsBase("/test/overlay") {
		t.Error("expected overlay to not be identified as base")
	}

	if g.IsBase("/test/nonexistent") {
		t.Error("expected nonexistent path to not be a base")
	}
}

func TestExtractDependencies(t *testing.T) {
	g := New().(*DependencyGraph)

	file := &discovery.KustomizeFile{
		Resources:  []string{"deployment.yaml", "../base", "service.yaml"},
		Bases:      []string{"../../common"},
		Components: []string{"../../components/monitoring"},
	}

	deps := g.extractDependencies(file)

	// Should have: ../base (from resources), ../../common (from bases), ../../components/monitoring (from components)
	// Should NOT have: deployment.yaml, service.yaml (they have extensions)
	expectedCount := 3
	if len(deps) != expectedCount {
		t.Errorf("expected %d dependencies, got %d: %v", expectedCount, len(deps), deps)
	}
}

func TestGetAllDependents(t *testing.T) {
	// Test recursive dependent lookup
	// Structure: base -> overlay1 -> overlay2
	//                 -> overlay3
	files := []discovery.KustomizeFile{
		{
			Dir:       "/test/base",
			Resources: []string{"deployment.yaml"},
		},
		{
			Dir:       "/test/overlay1",
			Resources: []string{"../base"},
		},
		{
			Dir:       "/test/overlay2",
			Resources: []string{"../overlay1"},
		},
		{
			Dir:       "/test/overlay3",
			Resources: []string{"../base"},
		},
	}

	g := New()
	if err := g.Build(files); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// When base changes, all dependents should be affected
	allDependents := g.GetAllDependents("/test/base")

	// Should get: overlay1, overlay2 (via overlay1), overlay3
	expectedCount := 3
	if len(allDependents) != expectedCount {
		t.Errorf("expected %d dependents for base, got %d: %v", expectedCount, len(allDependents), allDependents)
	}

	// Verify all expected dependents are present
	expectedDependents := map[string]bool{
		"/test/overlay1": false,
		"/test/overlay2": false,
		"/test/overlay3": false,
	}

	for _, dep := range allDependents {
		cleanDep := filepath.Clean(dep)
		if _, exists := expectedDependents[cleanDep]; exists {
			expectedDependents[cleanDep] = true
		}
	}

	for path, found := range expectedDependents {
		if !found {
			t.Errorf("expected to find dependent %s", path)
		}
	}

	// When overlay1 changes, only overlay2 should be affected (not overlay3)
	overlay1Dependents := g.GetAllDependents("/test/overlay1")
	if len(overlay1Dependents) != 1 {
		t.Errorf("expected 1 dependent for overlay1, got %d: %v", len(overlay1Dependents), overlay1Dependents)
	}

	if filepath.Clean(overlay1Dependents[0]) != "/test/overlay2" {
		t.Errorf("expected overlay2 as dependent of overlay1, got %s", overlay1Dependents[0])
	}
}

func TestGetAllDependentsNoCycles(t *testing.T) {
	// Test that cycles don't cause infinite loops
	// This shouldn't happen in real kustomize but we should handle it gracefully
	g := New().(*DependencyGraph)

	g.nodes = map[string]*Node{
		"/test/a": {Path: "/test/a", Dependencies: []string{"../b"}},
		"/test/b": {Path: "/test/b", Dependencies: []string{"../c"}},
		"/test/c": {Path: "/test/c", Dependencies: []string{"../a"}},
	}

	g.reverseLookup = map[string][]string{
		"/test/a": {"/test/c"},
		"/test/b": {"/test/a"},
		"/test/c": {"/test/b"},
	}

	// Should not hang or panic
	dependents := g.GetAllDependents("/test/a")

	// Should handle cycle gracefully
	if len(dependents) > 3 {
		t.Errorf("cycle handling failed, got too many dependents: %d", len(dependents))
	}
}
