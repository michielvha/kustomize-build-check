package graph

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/michielvha/kustomize-build-check/internal/discovery"
)

// Node represents a kustomization in the dependency graph
type Node struct {
	Path         string
	IsBase       bool
	Dependencies []string // Paths this node depends on
}

// DependencyGraph represents the relationship between kustomizations
type DependencyGraph struct {
	nodes         map[string]*Node
	reverseLookup map[string][]string // base -> [overlays that depend on it]
}

// Graph interface for dependency operations
type Graph interface {
	Build(files []discovery.KustomizeFile) error
	GetDependentOverlays(basePath string) []string
	GetAllDependents(path string) []string
	IsBase(path string) bool
	GetNode(path string) *Node
}

// New creates a new dependency graph
func New() Graph {
	return &DependencyGraph{
		nodes:         make(map[string]*Node),
		reverseLookup: make(map[string][]string),
	}
}

// Build constructs the dependency graph from discovered kustomization files
func (g *DependencyGraph) Build(files []discovery.KustomizeFile) error {
	// First pass: create all nodes
	for _, file := range files {
		g.nodes[file.Dir] = &Node{
			Path:         file.Dir,
			IsBase:       false,
			Dependencies: []string{},
		}
	}

	// Second pass: establish dependencies
	for _, file := range files {
		deps := g.extractDependencies(&file)

		node := g.nodes[file.Dir]
		node.Dependencies = deps

		// For each dependency, mark it as a base and add reverse lookup
		for _, dep := range deps {
			// Resolve relative path to absolute
			absDepPath := filepath.Join(file.Dir, dep)
			absDepPath = filepath.Clean(absDepPath)

			// Check if this dependency is a kustomization directory
			if depNode, exists := g.nodes[absDepPath]; exists {
				depNode.IsBase = true
				g.reverseLookup[absDepPath] = append(g.reverseLookup[absDepPath], file.Dir)
			}
		}
	}

	return nil
}

// extractDependencies extracts all dependency paths from a kustomization file
func (g *DependencyGraph) extractDependencies(file *discovery.KustomizeFile) []string {
	var deps []string

	// Check resources for kustomization directories
	for _, resource := range file.Resources {
		// Skip if it's a file (has extension)
		if filepath.Ext(resource) != "" {
			continue
		}

		// This might be a directory reference
		deps = append(deps, resource)
	}

	// Add deprecated bases field
	deps = append(deps, file.Bases...)

	// Add components
	deps = append(deps, file.Components...)

	return deps
}

// GetDependentOverlays returns all overlays that depend on the given base path
func (g *DependencyGraph) GetDependentOverlays(basePath string) []string {
	basePath = filepath.Clean(basePath)

	if overlays, exists := g.reverseLookup[basePath]; exists {
		// Return a copy to prevent external modification
		result := make([]string, len(overlays))
		copy(result, overlays)
		return result
	}

	return []string{}
}

// GetAllDependents recursively returns all kustomizations that depend on the given path
// This traverses up the dependency tree to find all consumers (direct and indirect)
func (g *DependencyGraph) GetAllDependents(path string) []string {
	path = filepath.Clean(path)
	
	visited := make(map[string]bool)
	result := []string{}
	
	// Recursive helper function
	var collectDependents func(currentPath string)
	collectDependents = func(currentPath string) {
		currentPath = filepath.Clean(currentPath)
		
		// Get direct dependents
		if dependents, exists := g.reverseLookup[currentPath]; exists {
			for _, dependent := range dependents {
				dependent = filepath.Clean(dependent)
				
				// Avoid cycles
				if visited[dependent] {
					continue
				}
				
				visited[dependent] = true
				result = append(result, dependent)
				
				// Recursively get dependents of this dependent
				collectDependents(dependent)
			}
		}
	}
	
	collectDependents(path)
	return result
}

// IsBase checks if the given path is a base (used by other kustomizations)
func (g *DependencyGraph) IsBase(path string) bool {
	path = filepath.Clean(path)

	if node, exists := g.nodes[path]; exists {
		return node.IsBase
	}

	return false
}

// GetNode returns the node for a given path
func (g *DependencyGraph) GetNode(path string) *Node {
	path = filepath.Clean(path)
	return g.nodes[path]
}

// String provides a human-readable representation of the graph
func (g *DependencyGraph) String() string {
	var sb strings.Builder

	sb.WriteString("Dependency Graph:\n")
	for path, node := range g.nodes {
		baseMarker := ""
		if node.IsBase {
			baseMarker = " [BASE]"
		}

		sb.WriteString(fmt.Sprintf("  %s%s\n", path, baseMarker))

		if len(node.Dependencies) > 0 {
			sb.WriteString("    Dependencies:\n")
			for _, dep := range node.Dependencies {
				sb.WriteString(fmt.Sprintf("      - %s\n", dep))
			}
		}

		if overlays := g.GetDependentOverlays(path); len(overlays) > 0 {
			sb.WriteString("    Used by:\n")
			for _, overlay := range overlays {
				sb.WriteString(fmt.Sprintf("      - %s\n", overlay))
			}
		}
	}

	return sb.String()
}
