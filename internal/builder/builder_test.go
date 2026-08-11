package builder

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildSkipsMissingDirectory is the unit-level guard for the reported bug:
// a directory removed by the change must be skipped, not reported as a failure.
func TestBuildSkipsMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "overlays", "removed")

	result := New().Build(missing, false)

	if !result.Skipped {
		t.Errorf("expected a skipped result, got %+v", result)
	}
	if result.Success {
		t.Error("a skipped path must not be reported as a success")
	}
	if result.SkipReason == "" {
		t.Error("a skipped result should carry a reason")
	}
	if result.Error != "" {
		t.Errorf("a skipped path must not carry a build error, got %q", result.Error)
	}
}

// TestBuildSkipsEmptyDirectory covers the leftover directory that moving the
// last file out of a kustomize directory leaves behind in a reused workspace.
// git cannot represent an empty directory, so there is nothing to validate.
func TestBuildSkipsEmptyDirectory(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "moved-away")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	result := New().Build(empty, false)

	if !result.Skipped {
		t.Errorf("expected an empty directory to be skipped, got %+v", result)
	}
	if result.Success {
		t.Error("a skipped path must not be reported as a success")
	}
}

// TestBuildDoesNotSkipExistingDirectoryWithoutKustomization guards against
// over-filtering. A directory that still exists but has lost its kustomization
// file is a genuine error and must be handed to kustomize, not skipped.
func TestBuildDoesNotSkipExistingDirectoryWithoutKustomization(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deployment.yaml"), []byte("kind: Deployment\n"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	result := New().Build(dir, false)

	if result.Skipped {
		t.Errorf("an existing directory must never be skipped, got %+v", result)
	}
}
