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

	if err := os.WriteFile(kustomizationPath, []byte(content), 0o644); err != nil {
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
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatalf("failed to create base dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "kustomization.yaml"), []byte("resources:\n  - deployment.yaml\n"), 0o644); err != nil {
		t.Fatalf("failed to write base kustomization: %v", err)
	}

	// overlays/dev/kustomization.yaml
	devDir := filepath.Join(tmpDir, "overlays", "dev")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatalf("failed to create dev dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(devDir, "kustomization.yaml"), []byte("resources:\n  - ../../base\n"), 0o644); err != nil {
		t.Fatalf("failed to write dev kustomization: %v", err)
	}

	// overlays/prod/kustomization.yml (different extension)
	prodDir := filepath.Join(tmpDir, "overlays", "prod")
	if err := os.MkdirAll(prodDir, 0o755); err != nil {
		t.Fatalf("failed to create prod dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(prodDir, "kustomization.yml"), []byte("resources:\n  - ../../base\n"), 0o644); err != nil {
		t.Fatalf("failed to write prod kustomization: %v", err)
	}

	d := New()
	files, err := d.FindAll(tmpDir)
	if err != nil {
		t.Fatalf("FindAll failed: %v", err)
	}

	if len(files) != 3 {
		t.Errorf("expected 3 kustomization files, got %d", len(files))
	}
}

// TestUnreadableFileIsFlaggedNotDropped covers the read-error half of F-C1.
// os.ReadFile fails before the YAML stage, so covering only malformed YAML would
// leave this case silently dropped.
func TestUnreadableFileIsFlaggedNotDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist.yaml"), path); err != nil {
		t.Fatalf("failed to create dangling symlink: %v", err)
	}

	kf, err := New().ParseKustomization(path)
	if err == nil {
		t.Fatal("expected an error for an unreadable file")
	}
	if kf == nil {
		t.Fatal("an unreadable file must still yield an entry, or the directory vanishes from the run")
	}
	if !kf.Unparsed || !kf.Unknown() {
		t.Errorf("an unreadable file must be Unparsed and Unknown, got %+v", kf)
	}
	if kf.Dir != dir {
		t.Errorf("Dir = %q, want %q", kf.Dir, dir)
	}
}

// TestMalformedYAMLIsFlaggedNotDropped covers the parse half of F-C1.
func TestMalformedYAMLIsFlaggedNotDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")
	if err := os.WriteFile(path, []byte("resources: [unclosed\n  : : :\n"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	kf, err := New().ParseKustomization(path)
	if err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
	if kf == nil || !kf.Unparsed {
		t.Fatalf("malformed YAML must yield a flagged entry, got %+v", kf)
	}
}

// TestUndecodableFieldDoesNotDropFile covers F-C6: one bad field costs that
// field's references, not the whole file.
func TestUndecodableFieldDoesNotDropFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")
	// `patches` as a mapping where this tool expects a list, alongside a
	// perfectly good resources field.
	content := "resources:\n  - deployment.yaml\nbases: {not: a-list}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	kf, err := New().ParseKustomization(path)
	if err != nil {
		t.Fatalf("a bad field must not fail the file: %v", err)
	}
	if kf.Unparsed {
		t.Error("a bad field must not mark the whole file Unparsed")
	}
	if len(kf.Resources) != 1 || kf.Resources[0] != "deployment.yaml" {
		t.Errorf("the good field must still parse, got %v", kf.Resources)
	}
	if len(kf.FieldErrs) != 1 || kf.FieldErrs[0].Field != "bases" {
		t.Errorf("expected one FieldError for bases, got %+v", kf.FieldErrs)
	}
	if !kf.Unknown() {
		t.Error("a field error means references are unknown, so Unknown() must be true")
	}
}

// TestUnknownFieldsAreIgnored covers F-C5: a kustomize feature this tool does
// not model must never fail a build.
func TestUnknownFieldsAreIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")
	content := "resources:\n  - deployment.yaml\nsomeFutureKustomizeFeature:\n  nested: {a: 1}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	kf, err := New().ParseKustomization(path)
	if err != nil {
		t.Fatalf("unknown fields must not error: %v", err)
	}
	if kf.Unknown() {
		t.Errorf("unknown fields must not make the file Unknown, got %+v", kf)
	}
}

// TestFindAllRetainsUnparseableFiles covers F-C2a: the entry survives discovery,
// so a node exists and dependents still propagate.
func TestFindAllRetainsUnparseableFiles(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "good")
	bad := filepath.Join(root, "bad")
	for _, d := range []string{good, bad} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(good, "kustomization.yaml"), []byte("resources: []\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bad, "kustomization.yaml"), []byte("resources: [unclosed\n : : :\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	files, err := New().FindAll(root)
	if err != nil {
		t.Fatalf("FindAll failed: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("both files must be retained, got %d: %+v", len(files), files)
	}
}
