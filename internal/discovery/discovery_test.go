package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsKustomizationFile(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{"standard yaml", "kustomization.yaml", true},
		{"standard yml", "kustomization.yml", true},
		{"capital K", "Kustomization", true},
		{"random yaml", "deployment.yaml", false},
		{"wrong name", "kustomize.yaml", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isKustomizationFile(tt.filename); got != tt.want {
				t.Errorf("isKustomizationFile(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestParseKustomization(t *testing.T) {
	// Create temp file
	tmpDir := t.TempDir()
	kustomizationPath := filepath.Join(tmpDir, "kustomization.yaml")

	content := `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - deployment.yaml
  - service.yaml
  - ../base
bases:
  - ../../common
components:
  - ../../components/monitoring
`

	if err := os.WriteFile(kustomizationPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	d := New()
	kf, err := d.ParseKustomization(kustomizationPath)
	if err != nil {
		t.Fatalf("ParseKustomization failed: %v", err)
	}

	if len(kf.Resources) != 3 {
		t.Errorf("expected 3 resources, got %d", len(kf.Resources))
	}

	if len(kf.Bases) != 1 {
		t.Errorf("expected 1 base, got %d", len(kf.Bases))
	}

	if len(kf.Components) != 1 {
		t.Errorf("expected 1 component, got %d", len(kf.Components))
	}

	if kf.Dir != tmpDir {
		t.Errorf("expected dir %s, got %s", tmpDir, kf.Dir)
	}
}

func TestFindAll(t *testing.T) {
	// Create test structure
	tmpDir := t.TempDir()

	// base/kustomization.yaml
	baseDir := filepath.Join(tmpDir, "base")
	os.MkdirAll(baseDir, 0755)
	os.WriteFile(filepath.Join(baseDir, "kustomization.yaml"), []byte("resources:\n  - deployment.yaml\n"), 0644)

	// overlays/dev/kustomization.yaml
	devDir := filepath.Join(tmpDir, "overlays", "dev")
	os.MkdirAll(devDir, 0755)
	os.WriteFile(filepath.Join(devDir, "kustomization.yaml"), []byte("resources:\n  - ../../base\n"), 0644)

	// overlays/prod/kustomization.yml (different extension)
	prodDir := filepath.Join(tmpDir, "overlays", "prod")
	os.MkdirAll(prodDir, 0755)
	os.WriteFile(filepath.Join(prodDir, "kustomization.yml"), []byte("resources:\n  - ../../base\n"), 0644)

	d := New()
	files, err := d.FindAll(tmpDir)
	if err != nil {
		t.Fatalf("FindAll failed: %v", err)
	}

	if len(files) != 3 {
		t.Errorf("expected 3 kustomization files, got %d", len(files))
	}
}
