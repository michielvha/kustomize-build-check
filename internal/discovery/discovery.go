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

// Ref is a single file reference, recording where it came from so an unexpected
// match can be traced back to the field that produced it.
type Ref struct {
	Field string // the kustomization field, e.g. "configMapGenerator.files"
	// Raw is the path this reference resolves against. For generator fields it
	// is the path portion after the "key=" alias is stripped, so it is not
	// always byte-identical to what the author wrote.
	Raw string
}

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

	// FileRefs is every local file this kustomization pulls in, from any field.
	// It is the single source the analyzer matches changed files against, so
	// there is exactly one matching path regardless of which field a reference
	// came from.
	FileRefs []Ref
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

		// Skip directories that cannot contain manifests we care about.
		//
		// Only .git is skipped, not every dot-prefixed directory. Skipping them
		// all made discovery and impact analysis disagree about which
		// kustomizations exist: the analyzer marks a directory by the basename of
		// a changed kustomization file, without consulting the discovered set, so
		// a kustomization under .deploy/ was validated in diff mode and invisible
		// to a full scan. Two stages with different universes is how a fallback
		// that is meant to be strictly safer ends up validating less.
		if entry.IsDir() && entry.Name() == ".git" && path != rootDir {
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

	kf.collectFileRefs(doc)

	return kf, nil
}

// collectFileRefs gathers every field that can name a local file.
//
// Directory-shaped fields (bases, components) are deliberately absent: those
// become graph edges, not file matches. `resources` appears in both because an
// entry may be either, and the resolved comparison sorts that out.
func (kf *KustomizeFile) collectFileRefs(doc map[string]yaml.Node) {
	kf.addRefs("resources", kf.Resources...)

	// patches[] entries are objects: {path, target} or an inline {patch}.
	// An entry with no path contributes nothing and is not an error.
	for _, p := range decodeObjectList(kf, doc, "patches") {
		kf.addRefs("patches.path", decodeField[string](kf, "patches", p, "path")...)
	}
	// patchesJson6902[] is the deprecated equivalent, same shape.
	for _, p := range decodeObjectList(kf, doc, "patchesJson6902") {
		kf.addRefs("patchesJson6902.path", decodeField[string](kf, "patchesJson6902", p, "path")...)
	}
	// patchesStrategicMerge[] is a plain list of paths (deprecated).
	kf.addRefs("patchesStrategicMerge", decodeStrings(kf, doc, "patchesStrategicMerge")...)

	// Generators: files and envs both name local files, both accept the
	// "key=path" alias form.
	for _, field := range []string{"configMapGenerator", "secretGenerator"} {
		for _, g := range decodeObjectList(kf, doc, field) {
			kf.addRefs(field+".files", decodeField[[]string](kf, field, g, "files")...)
			kf.addRefs(field+".envs", decodeField[[]string](kf, field, g, "envs")...)
			// `env` is the deprecated singular form.
			kf.addRefs(field+".env", decodeField[string](kf, field, g, "env")...)
		}
	}

	// helmCharts[] valuesFile / additionalValuesFiles are local files whose
	// contents change the rendered output.
	for _, h := range decodeObjectList(kf, doc, "helmCharts") {
		kf.addRefs("helmCharts.valuesFile", decodeField[string](kf, "helmCharts", h, "valuesFile")...)
		kf.addRefs("helmCharts.additionalValuesFiles", decodeField[[]string](kf, "helmCharts", h, "additionalValuesFiles")...)
	}

	// Lower-traffic surfaces that still resolve local files.
	kf.addRefs("crds", decodeStrings(kf, doc, "crds")...)
	kf.addRefs("configurations", decodeStrings(kf, doc, "configurations")...)
	// openapi is a mapping, not a list (verified against kustomize v5.8.1).
	if o := decodeObject(kf, doc, "openapi"); o != nil {
		kf.addRefs("openapi.path", decodeField[string](kf, "openapi", o, "path")...)
	}
}

// addRefs normalises and records references, dropping the ones that cannot name
// a local file.
func (kf *KustomizeFile) addRefs(field string, refs ...string) {
	for _, raw := range refs {
		if raw == "" {
			continue
		}
		// Remote references are never local files, so they can never be matched
		// against a changed path. Skipping them keeps them out of the resolver,
		// where they would resolve to a nonsense path under the kustomization.
		if strings.Contains(raw, "://") || strings.HasPrefix(raw, "git@") {
			slog.Debug("Skipping non-local reference", "field", field, "ref", raw)
			continue
		}
		kf.FileRefs = append(kf.FileRefs, Ref{Field: field, Raw: stripAlias(field, raw)})
	}
}

// stripAlias handles the "key=path" form accepted by generator files/envs.
//
// Only generator fields use it. Applying it everywhere would corrupt a legal
// filename containing "=" in fields that have no alias form — which is exactly
// the trap: a naive parser looks for a file literally named "aliaskey=real.txt".
func stripAlias(field, raw string) string {
	if !strings.Contains(field, ".files") && !strings.Contains(field, ".envs") && !strings.HasSuffix(field, ".env") {
		return raw
	}
	key, path, found := strings.Cut(raw, "=")
	if !found || key == "" {
		// No alias, or a leading "=" — treat the whole string as the path. A
		// filename containing "=" with no alias is an accepted limitation.
		return raw
	}
	slog.Debug("Split generator alias", "field", field, "raw", raw, "path", path)
	return path
}

// decodeObjectList decodes a list-of-mappings field, recording a FieldError
// rather than failing the file when the shape is wrong.
func decodeObjectList(kf *KustomizeFile, doc map[string]yaml.Node, field string) []map[string]yaml.Node {
	node, ok := doc[field]
	if !ok {
		return nil
	}
	var out []map[string]yaml.Node
	if err := node.Decode(&out); err != nil {
		kf.FieldErrs = append(kf.FieldErrs, FieldError{Field: field, Err: err})
		return nil
	}
	return out
}

// decodeObject decodes a single mapping field.
func decodeObject(kf *KustomizeFile, doc map[string]yaml.Node, field string) map[string]yaml.Node {
	node, ok := doc[field]
	if !ok {
		return nil
	}
	var out map[string]yaml.Node
	if err := node.Decode(&out); err != nil {
		kf.FieldErrs = append(kf.FieldErrs, FieldError{Field: field, Err: err})
		return nil
	}
	return out
}

// decodeField pulls one typed value out of a decoded mapping.
//
// A *missing* key yields nothing and is not an error: these are optional by
// design, since a patches entry may carry an inline `patch` and no `path`.
//
// A key that is *present but the wrong shape* is different, and must be
// recorded. Returning nil silently would mean the tool learned no references
// from that field while believing it had learned all of them — which is the
// false pass this whole change exists to close (F-C6). The FieldError makes the
// directory always-affected, so kustomize adjudicates.
func decodeField[T any](kf *KustomizeFile, field string, obj map[string]yaml.Node, key string) []string {
	node, ok := obj[key]
	if !ok {
		return nil
	}
	var v T
	if err := node.Decode(&v); err != nil {
		kf.FieldErrs = append(kf.FieldErrs, FieldError{Field: field + "." + key, Err: err})
		return nil
	}
	switch typed := any(v).(type) {
	case string:
		return []string{typed}
	case []string:
		return typed
	}
	return nil
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
