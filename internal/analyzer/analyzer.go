package analyzer

import (
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
	affected := make(map[string]bool)

	for _, changedFile := range changedFiles {
		// Check if the changed file is a kustomization file itself
		if isKustomizationFile(filepath.Base(changedFile)) {
			dir := filepath.Dir(changedFile)
			a.addAffected(dir, g, affected)
			continue
		}

		// Check if the changed file is referenced by any kustomization
		for _, kust := range allKustomizations {
			if a.fileReferencedByKustomization(changedFile, kust) {
				a.addAffected(kust.Dir, g, affected)
			}
		}
	}

	// Convert map to slice
	result := make([]string, 0, len(affected))
	for path := range affected {
		result = append(result, path)
	}

	return result
}

// addAffected adds a kustomization and potentially its dependents to the affected set
func (a *analyzer) addAffected(dir string, g graph.Graph, affected map[string]bool) {
	dir = filepath.Clean(dir)

	// If this is a base, test all overlays that depend on it
	if g.IsBase(dir) {
		overlays := g.GetDependentOverlays(dir)
		for _, overlay := range overlays {
			affected[filepath.Clean(overlay)] = true
		}
	} else {
		// If it's an overlay, just test it
		affected[dir] = true
	}
}

// fileReferencedByKustomization checks if a file is referenced by a kustomization
func (a *analyzer) fileReferencedByKustomization(changedFile string, kust discovery.KustomizeFile) bool {
	changedFile = filepath.Clean(changedFile)
	kustDir := filepath.Clean(kust.Dir)

	// Check if the changed file is in the same directory or subdirectory
	if !strings.HasPrefix(changedFile, kustDir) {
		return false
	}

	// Check if this relative path is in resources
	for _, resource := range kust.Resources {
		// Resource could be a file or directory
		resourcePath := filepath.Clean(filepath.Join(kustDir, resource))

		// Check if changed file is the resource or inside a resource directory
		if changedFile == resourcePath || strings.HasPrefix(changedFile, resourcePath+string(filepath.Separator)) {
			return true
		}
	}

	return false
}

// isKustomizationFile checks if a filename is a kustomization file
func isKustomizationFile(name string) bool {
	return name == "kustomization.yaml" ||
		name == "kustomization.yml" ||
		name == "Kustomization"
}
