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
