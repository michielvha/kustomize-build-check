package discovery

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FieldError records that a single field could not be decoded. The rest of the
// file is still usable; only this field yielded no references.
type FieldError struct {
	Field string // the kustomization field, e.g. "resources"
	Err   error  // why it could not be decoded
}

func (e FieldError) Error() string { return e.Field + ": " + e.Err.Error() }

// KustomizeFile represents a parsed kustomization file
type KustomizeFile struct {
	Path       string   // Absolute path to kustomization.yaml
	Dir        string   // Directory containing the file
	Resources  []string // Relative paths referenced
	Bases      []string // Deprecated bases field
	Components []string // Component paths

	// Unparsed marks a file this tool could not read or decode at all. The entry
	// is still returned — dropping it is how a broken kustomization used to
	// produce a green check — but its references are unknown, so callers must
	// treat it as always affected and let `kustomize build` decide.
	Unparsed bool
	// ParseErr is why the file is Unparsed. Nil when Unparsed is false.
	ParseErr error
	// FieldErrs records fields that could not be decoded while the file as a
	// whole was fine. Like Unparsed, a non-empty FieldErrs means the tool does
	// not know what those fields referenced, so the directory must be treated as
	// always affected rather than as having no references.
	FieldErrs []FieldError
}

// Unknown reports whether anything about this file's references could not be
// determined, whether the whole file or a single field. Callers use this to
// decide that a directory must be validated regardless of the diff.
func (k KustomizeFile) Unknown() bool {
	return k.Unparsed || len(k.FieldErrs) > 0
}

// Discoverer finds and parses kustomization files
type Discoverer interface {
	FindAll(rootDir string) ([]KustomizeFile, error)
	ParseKustomization(path string) (*KustomizeFile, error)
}

type discoverer struct{}

// New creates a new Discoverer
func New() Discoverer {
	return &discoverer{}
}

// FindAll recursively finds all kustomization files in rootDir
func (d *discoverer) FindAll(rootDir string) ([]KustomizeFile, error) {
	var files []KustomizeFile

	err := filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".") && path != rootDir {
			return fs.SkipDir
		}

		// Check if this is a kustomization file
		if !entry.IsDir() && isKustomizationFile(entry.Name()) {
			kf, err := d.ParseKustomization(path)
			if kf == nil {
				// Only an unresolvable absolute path gets here, so there is no
				// directory to hand back and nothing useful to report.
				slog.Warn("Skipping kustomization file with unresolvable path",
					"path", path, "error", err)
				return nil
			}
			// A parse failure is deliberately NOT a reason to drop the entry.
			// Dropping it removed the directory from the run entirely, so a
			// broken kustomization produced a green check. The entry is kept,
			// flagged, and reported; the caller treats it as always affected.
			if err != nil {
				slog.Debug("Retaining unparseable kustomization",
					"path", path, "error", err)
			}
			files = append(files, *kf)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}

	return files, nil
}

// ParseKustomization parses a kustomization file.
//
// It decodes in two stages so that one bad field cannot discard a whole file:
// stage 1 decodes the document into a generic mapping, stage 2 decodes each
// field this tool understands. A stage 2 failure records a FieldError and yields
// no references from that field; every other field still parses.
//
// On failure it returns a **non-nil** *KustomizeFile alongside the error, marked
// Unparsed. That matters: the directory is what the caller needs in order to
// validate it anyway. Returning only an error is how an unreadable or malformed
// kustomization used to vanish from the run and let it exit 0.
func (d *discoverer) ParseKustomization(path string) (*KustomizeFile, error) {
	absPath, absErr := filepath.Abs(path)
	if absErr != nil {
		// Without an absolute path there is no directory to report, so this is
		// the one case that cannot degrade into an Unparsed entry.
		return nil, fmt.Errorf("failed to get absolute path: %w", absErr)
	}

	kf := &KustomizeFile{Path: absPath, Dir: filepath.Dir(absPath)}

	// A read failure never reaches the YAML stage: a dangling symlink or an
	// unreadable file fails here. It is still "cannot be read or parsed", so it
	// must flag the file rather than drop it.
	data, err := os.ReadFile(path)
	if err != nil {
		kf.Unparsed = true
		kf.ParseErr = fmt.Errorf("failed to read file: %w", err)
		return kf, kf.ParseErr
	}

	// Stage 1: the document itself. Only genuinely malformed YAML fails here.
	var doc map[string]yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		kf.Unparsed = true
		kf.ParseErr = fmt.Errorf("failed to parse YAML: %w", err)
		return kf, kf.ParseErr
	}

	// Stage 2: each field this tool models. An undecodable field costs that
	// field's references, not the file. Unknown fields are ignored, so a
	// kustomize feature this tool does not model never fails a build.
	kf.Resources = decodeStrings(kf, doc, "resources")
	kf.Bases = decodeStrings(kf, doc, "bases")
	kf.Components = decodeStrings(kf, doc, "components")

	return kf, nil
}

// decodeStrings decodes one string-list field, recording a FieldError instead of
// failing the file when the shape is not what this tool expects.
func decodeStrings(kf *KustomizeFile, doc map[string]yaml.Node, field string) []string {
	node, ok := doc[field]
	if !ok {
		return nil
	}

	var out []string
	if err := node.Decode(&out); err != nil {
		kf.FieldErrs = append(kf.FieldErrs, FieldError{Field: field, Err: err})
		return nil
	}
	return out
}

// isKustomizationFile checks if the filename is a kustomization file
func isKustomizationFile(name string) bool {
	return name == "kustomization.yaml" ||
		name == "kustomization.yml" ||
		name == "Kustomization"
}
