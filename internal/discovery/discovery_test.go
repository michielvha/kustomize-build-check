package discovery

import (
	"os"
	"path/filepath"
	"slices"
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

// TestFileRefsSupersetOfResources is the guard for the one narrowing step in
// this work: the analyzer switched from matching Resources to matching FileRefs,
// so FileRefs must never lose a *matchable* entry Resources had.
//
// "Matchable" is the precise claim. Remote references are deliberately dropped
// from FileRefs, which loses nothing: a changed path is always local, so a
// remote reference could never have matched one.
func TestFileRefsSupersetOfResources(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")
	// Includes a filename containing "=", which the alias splitter must NOT
	// touch outside generator fields.
	content := "resources:\n  - deployment.yaml\n  - ../base\n  - weird=name.yaml\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	kf, err := New().ParseKustomization(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, want := range kf.Resources {
		found := false
		for _, ref := range kf.FileRefs {
			if ref.Raw == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("resources entry %q is missing from FileRefs: %+v", want, kf.FileRefs)
		}
	}
}

// TestGeneratorAliasIsSplit covers the alias trap (AC-D4). `files` accepts a
// "key=path" form, so a naive parser looks for a file literally named
// "aliaskey=real-file.txt". Deliberately its own test, not folded into a table.
func TestGeneratorAliasIsSplit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")
	content := "configMapGenerator:\n  - name: cm\n    files:\n      - aliaskey=real-file.txt\n      - plain.txt\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	kf, err := New().ParseKustomization(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var raws []string
	for _, ref := range kf.FileRefs {
		raws = append(raws, ref.Raw)
	}
	for _, want := range []string{"real-file.txt", "plain.txt"} {
		if !slices.Contains(raws, want) {
			t.Errorf("expected %q in FileRefs, got %v", want, raws)
		}
	}
	if slices.Contains(raws, "aliaskey=real-file.txt") {
		t.Errorf("the alias must be stripped, got %v", raws)
	}
}

// TestAliasSplitterDoesNotTouchNonGeneratorFields guards the inverse: a legal
// filename containing "=" in a field with no alias form must survive intact.
func TestAliasSplitterDoesNotTouchNonGeneratorFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")
	if err := os.WriteFile(path, []byte("resources:\n  - weird=name.yaml\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	kf, err := New().ParseKustomization(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var raws []string
	for _, ref := range kf.FileRefs {
		raws = append(raws, ref.Raw)
	}
	if !slices.Contains(raws, "weird=name.yaml") {
		t.Errorf("a filename containing = must survive outside generator fields, got %v", raws)
	}
}

// TestReferenceSurfaces covers one scenario per field family (F-D1..F-D8).
func TestReferenceSurfaces(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"patches path", "patches:\n  - path: patch.yaml\n", "patch.yaml"},
		{"patchesStrategicMerge", "patchesStrategicMerge:\n  - legacy.yaml\n", "legacy.yaml"},
		{"patchesJson6902", "patchesJson6902:\n  - path: j6902.yaml\n    target: {kind: Deployment}\n", "j6902.yaml"},
		{"configMapGenerator files", "configMapGenerator:\n  - name: cm\n    files:\n      - cm-file.txt\n", "cm-file.txt"},
		{"configMapGenerator envs", "configMapGenerator:\n  - name: cm\n    envs:\n      - cm.env\n", "cm.env"},
		{"secretGenerator envs", "secretGenerator:\n  - name: s\n    envs:\n      - sec.env\n", "sec.env"},
		{"helmCharts valuesFile", "helmCharts:\n  - name: demo\n    valuesFile: values.yaml\n", "values.yaml"},
		{"helmCharts additionalValuesFiles", "helmCharts:\n  - name: demo\n    additionalValuesFiles:\n      - extra.yaml\n", "extra.yaml"},
		{"crds", "crds:\n  - crd.json\n", "crd.json"},
		{"configurations", "configurations:\n  - namereference.yaml\n", "namereference.yaml"},
		{"openapi path", "openapi:\n  path: schema.json\n", "schema.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "kustomization.yaml")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}

			kf, err := New().ParseKustomization(path)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var raws []string
			for _, ref := range kf.FileRefs {
				raws = append(raws, ref.Raw)
			}
			if !slices.Contains(raws, tt.want) {
				t.Errorf("expected %q in FileRefs, got %v (field errors: %+v)", tt.want, raws, kf.FieldErrs)
			}
		})
	}
}

// TestInlinePatchWithoutPathIsNotAnError covers F-D2: a patches entry may carry
// an inline `patch` and no `path`, which contributes nothing and is legal.
func TestInlinePatchWithoutPathIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")
	content := "patches:\n  - patch: |-\n      - op: replace\n        path: /spec/replicas\n        value: 3\n    target: {kind: Deployment}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	kf, err := New().ParseKustomization(path)
	if err != nil {
		t.Fatalf("an inline patch must not error: %v", err)
	}
	if kf.Unknown() {
		t.Errorf("an inline patch must not make the file Unknown, got %+v", kf)
	}
	if len(kf.FileRefs) != 0 {
		t.Errorf("an inline patch contributes no file refs, got %+v", kf.FileRefs)
	}
}

// TestRemoteReferencesAreSkipped covers F-D12: a remote reference is never a
// local file, so it must not be resolved against the kustomization directory.
func TestRemoteReferencesAreSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")
	content := "resources:\n  - https://example.com/manifest.yaml\n  - git@github.com:org/repo.git//overlay\n  - local.yaml\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	kf, err := New().ParseKustomization(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(kf.FileRefs) != 1 || kf.FileRefs[0].Raw != "local.yaml" {
		t.Errorf("only the local reference may become a FileRef, got %+v", kf.FileRefs)
	}
}

// TestWrongShapeFieldIsRecordedNotFatal covers F-C6 for the new surfaces.
func TestWrongShapeFieldIsRecordedNotFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")
	if err := os.WriteFile(path, []byte("resources:\n  - ok.yaml\nconfigMapGenerator: \"oops\"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	kf, err := New().ParseKustomization(path)
	if err != nil {
		t.Fatalf("a wrong-shape field must not fail the file: %v", err)
	}
	if kf.Unparsed {
		t.Error("a wrong-shape field must not mark the file Unparsed")
	}
	if len(kf.FieldErrs) == 0 {
		t.Error("a wrong-shape field must record a FieldError")
	}
	if !kf.Unknown() {
		t.Error("a field error means references are unknown")
	}
}

// TestWrongShapedValueInsideFieldIsRecorded closes the F-C6 hole the shipped-code
// review found: a value that is present but the wrong shape used to be swallowed,
// so the tool learned no references from that field while believing it had
// learned them all. That is a false pass of exactly the class Phase 3 closed.
func TestWrongShapedValueInsideFieldIsRecorded(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"patches path as a list", "patches:\n  - path: [a, b]\n"},
		{"generator files as a string", "configMapGenerator:\n  - name: cm\n    files: \"notalist\"\n"},
		{"helmCharts valuesFile as a list", "helmCharts:\n  - name: d\n    valuesFile: [x]\n"},
		{"openapi path as a list", "openapi:\n  path: [x]\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "kustomization.yaml")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}

			kf, err := New().ParseKustomization(path)
			if err != nil {
				t.Fatalf("a wrong-shaped value must not fail the whole file: %v", err)
			}
			if len(kf.FieldErrs) == 0 {
				t.Errorf("a wrong-shaped value must record a FieldError, got %+v", kf)
			}
			if !kf.Unknown() {
				t.Error("references are unknown, so the directory must be always-affected")
			}
		})
	}
}

// TestDotDirectoriesAreDiscovered covers AC-P13. Discovery and impact analysis
// must share one universe: the analyzer marks a directory by the basename of a
// changed kustomization file without consulting the discovered set, so anything
// discovery cannot see is validated in diff mode and invisible to a full scan.
func TestDotDirectoriesAreDiscovered(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".deploy", "app", ".git"} {
		full := filepath.Join(root, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(full, "kustomization.yaml"), []byte("resources: []\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	files, err := New().FindAll(root)
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}

	var dirs []string
	for _, kf := range files {
		dirs = append(dirs, filepath.Base(kf.Dir))
	}
	if !slices.Contains(dirs, ".deploy") {
		t.Errorf("a kustomization under a dot-directory must be discovered, got %v", dirs)
	}
	if !slices.Contains(dirs, "app") {
		t.Errorf("ordinary directories must still be discovered, got %v", dirs)
	}
	if slices.Contains(dirs, ".git") {
		t.Errorf(".git must still be skipped, got %v", dirs)
	}
}
