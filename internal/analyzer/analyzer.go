package analyzer

import (
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/michielvha/kustomize-build-check/internal/discovery"
	"github.com/michielvha/kustomize-build-check/internal/graph"
)

// ImpactAnalyzer determines which kustomizations need testing
type ImpactAnalyzer interface {
	GetAffectedKustomizations(
		changedFiles []string,
		g graph.Graph,
		allKustomizations []discovery.KustomizeFile,
	) []string
}

type analyzer struct{}

// New creates a new impact analyzer
func New() ImpactAnalyzer {
	return &analyzer{}
}

// GetAffectedKustomizations analyzes changed files and returns kustomizations to test
func (a *analyzer) GetAffectedKustomizations(
	changedFiles []string,
	g graph.Graph,
	allKustomizations []discovery.KustomizeFile,
) []string {
	slog.Debug("Analyzing impact of changed files", "changed_files_count", len(changedFiles))

	affected := make(map[string]bool)

	// A kustomization whose references could not be determined is validated
	// regardless of what changed. The tool does not know what it depends on, so
	// it cannot know that the change missed it — and guessing "nothing" is how a
	// broken kustomization produced a green check.
	//
	// Deliberately without dependent propagation: what is unknown is this file's
	// *outgoing* references. Edges pointing at it come from other files, which
	// parsed fine, and already propagate through the graph.
	for _, kust := range allKustomizations {
		if !kust.Unknown() {
			continue
		}
		slog.Debug("Kustomization references are unknown, marking always affected",
			"kustomization", kust.Dir,
			"unparsed", kust.Unparsed,
			"field_errors", len(kust.FieldErrs))
		affected[filepath.Clean(kust.Dir)] = true
	}

	for _, changedFile := range changedFiles {
		slog.Debug("Processing changed file", "file", changedFile)

		// git reports paths relative to the repository root, while discovery
		// records absolute directories. Normalize once, up front, so both
		// branches below compare like with like.
		absFile, err := filepath.Abs(changedFile)
		if err != nil {
			// Fall back to the relative path if abs fails
			absFile = filepath.Clean(changedFile)
		}

		// Check if the changed file is a kustomization file itself
		if isKustomizationFile(filepath.Base(changedFile)) {
			absDir := filepath.Dir(absFile)
			slog.Debug("Changed file is kustomization file",
				"file", changedFile,
				"dir", absDir)
			a.addAffected(absDir, g, affected)
			continue
		}

		// Check if the changed file is referenced by any kustomization
		for _, kust := range allKustomizations {
			if match, ok := a.fileReferencedByKustomization(absFile, kust); ok {
				// Log the reference that caused the match, not just the fact of
				// it. Without the raw and resolved forms, an unexpected match is
				// not diagnosable from the log alone.
				slog.Debug("Changed file referenced by kustomization",
					"file", changedFile,
					"kustomization", kust.Dir,
					"field", match.Field,
					"reference", match.Raw,
					"resolved", match.Resolved)
				a.addAffected(kust.Dir, g, affected)
			}
		}
	}

	// Convert map to slice
	result := make([]string, 0, len(affected))
	for path := range affected {
		result = append(result, path)
	}

	slog.Debug("Impact analysis complete",
		"affected_kustomizations", len(result))

	return result
}

// addAffected adds a kustomization and all its dependents to the affected set
func (a *analyzer) addAffected(dir string, g graph.Graph, affected map[string]bool) {
	dir = filepath.Clean(dir)

	// Always add the directly affected kustomization
	affected[dir] = true
	slog.Debug("Added affected kustomization", "path", dir)

	// Recursively add all kustomizations that depend on this one
	// This catches the full impact chain: if a base changes, test all overlays,
	// and if those overlays are also bases, test their dependents too
	dependents := g.GetAllDependents(dir)

	if len(dependents) > 0 {
		slog.Debug("Adding dependents to affected set",
			"base", dir,
			"dependent_count", len(dependents))
	}

	for _, dependent := range dependents {
		cleanDep := filepath.Clean(dependent)
		affected[cleanDep] = true
		slog.Debug("Added dependent to affected set", "path", cleanDep)
	}
}

// referenceMatch describes which reference caused a match, so the caller can
// report it. Both forms are needed: the raw string is what the author wrote,
// the resolved path is what it actually points at.
type referenceMatch struct {
	Field    string // the kustomization field it came from, e.g. "patches.path"
	Raw      string // the reference exactly as written, e.g. "../shared/cm.yaml"
	Resolved string // absolute, cleaned, e.g. "/repo/shared/cm.yaml"
}

// fileReferencedByKustomization reports whether a kustomization references the
// changed file, and through which reference.
//
// changedFile must be an absolute path, to match the absolute Dir recorded by
// discovery.
//
// Matching is decided *solely* by comparing the changed path against each
// resolved reference. There is deliberately no check that the changed file
// lives under the kustomization's own directory: a kustomization may legitimately
// reference a file anywhere in the tree ("../shared/cm.yaml"), and such a
// containment pre-check was the sole reason those references were never matched
// — the tool reported green having validated nothing. See G1 and G5 in
// docs/specs/complete-impact-matching.spec.md.
//
// Precision comes from the resolved comparison itself, which is exact or
// separator-terminated, so a resolved reference of "/repo/shared" never matches
// "/repo/shared-old/x.yaml".
func (a *analyzer) fileReferencedByKustomization(changedFile string, kust discovery.KustomizeFile) (referenceMatch, bool) {
	changedFile = filepath.Clean(changedFile)
	kustDir := filepath.Clean(kust.Dir)

	// FileRefs is the single matching path: every field that can name a local
	// file feeds it, so there is no second set of rules for patches, generators
	// or helm values.
	for _, ref := range kust.FileRefs {
		// A reference may be a file or a directory, and may escape kustDir.
		resolved := filepath.Clean(filepath.Join(kustDir, ref.Raw))

		// The changed file is the reference itself, or lives inside it. The
		// trailing separator is load-bearing: without it "/repo/shared" would
		// match "/repo/shared-old/x.yaml".
		if changedFile == resolved ||
			strings.HasPrefix(changedFile, resolved+string(filepath.Separator)) {
			return referenceMatch{Field: ref.Field, Raw: ref.Raw, Resolved: resolved}, true
		}
	}

	return referenceMatch{}, false
}

// isKustomizationFile checks if a filename is a kustomization file
func isKustomizationFile(name string) bool {
	return name == "kustomization.yaml" ||
		name == "kustomization.yml" ||
		name == "Kustomization"
}
