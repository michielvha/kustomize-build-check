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

	for _, changedFile := range changedFiles {
		slog.Debug("Processing changed file", "file", changedFile)
		
		// Check if the changed file is a kustomization file itself
		if isKustomizationFile(filepath.Base(changedFile)) {
			dir := filepath.Dir(changedFile)
			// Convert to absolute path to match graph nodes
			absDir, err := filepath.Abs(dir)
			if err != nil {
				// Fall back to relative if abs fails
				absDir = dir
			}
			slog.Debug("Changed file is kustomization file", 
				"file", changedFile, 
				"dir", absDir)
			a.addAffected(absDir, g, affected)
			continue
		}

		// Check if the changed file is referenced by any kustomization
		for _, kust := range allKustomizations {
			if a.fileReferencedByKustomization(changedFile, kust) {
				slog.Debug("Changed file referenced by kustomization", 
					"file", changedFile, 
					"kustomization", kust.Dir)
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
