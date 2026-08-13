package graph

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/michielvha/kustomize-build-check/internal/discovery"
)

// Node represents a kustomization in the dependency graph
type Node struct {
	Path   string
	IsBase bool
	// Dependencies holds every reference as written in the kustomization
	// (resources, bases and components), before resolution. It is a record of
	// what was referenced, not of which edges exist: an edge exists only where
	// the resolved path is a discovered kustomization. See extractDependencies.
	Dependencies []string
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
	slog.Debug("Building dependency graph", "kustomization_count", len(files))

	// First pass: create all nodes
	for _, file := range files {
		g.nodes[file.Dir] = &Node{
			Path:         file.Dir,
			IsBase:       false,
			Dependencies: []string{},
		}
		slog.Debug("Created node", "path", file.Dir)
	}

	// Second pass: establish dependencies
	for _, file := range files {
		deps := g.extractDependencies(&file)

		node := g.nodes[file.Dir]
		node.Dependencies = deps

		if len(deps) > 0 {
			slog.Debug("Found dependencies", "kustomization", file.Dir, "dependencies", deps)
		}

		// For each dependency, mark it as a base and add reverse lookup
		for _, dep := range deps {
			// Resolve relative path to absolute
			absDepPath := filepath.Join(file.Dir, dep)
			absDepPath = filepath.Clean(absDepPath)

			// The reverse edge is recorded whether or not a kustomization was
			// discovered at the resolved path.
			//
			// Recording it only for discovered nodes looks right but hides a
			// real breakage: discovery walks the *post-change* tree, so a base
			// the change deleted has no node, gets no reverse edge, and its
			// surviving overlays are never marked affected. The run then reports
			// success while `kustomize build` on those overlays fails.
			//
			// This cannot over-match. addAffected is only ever called with a
			// kustomization directory, so a reverse key that names something
			// which is not one (a plain manifest file, say) is simply never
			// looked up.
			g.reverseLookup[absDepPath] = append(g.reverseLookup[absDepPath], file.Dir)

			if depNode, exists := g.nodes[absDepPath]; exists {
				depNode.IsBase = true
				slog.Debug("Added reverse lookup",
					"base", absDepPath,
					"dependent", file.Dir)
			} else {
				slog.Debug("Added reverse lookup for an undiscovered path",
					"dependency", absDepPath,
					"referenced_by", file.Dir,
					"note", "deleted base, or a file reference that is not a kustomization")
			}
		}
	}

	slog.Debug("Dependency graph built",
		"total_nodes", len(g.nodes),
		"bases", len(g.reverseLookup))

	return nil
}

// extractDependencies returns every reference that could name another
// kustomization, exactly as written.
//
// It deliberately does not try to tell a file reference from a directory
// reference. That question is answered later, and correctly, by asking whether a
// kustomization was actually discovered at the resolved path (see Build). The
// previous `filepath.Ext(resource) != ""` heuristic guessed from the name and
// got it wrong for any directory containing a dot: `filepath.Ext("../bases/v1.2")`
// is ".2" and `filepath.Ext("../my.app")` is ".app", so both were treated as
// files and their base-to-overlay edges were silently dropped.
//
// Returning file entries here is harmless: a resolved path only becomes an edge
// if a discovered kustomization sits there, and a plain manifest file never
// does. See ADR-001.
func (g *DependencyGraph) extractDependencies(file *discovery.KustomizeFile) []string {
	deps := make([]string, 0, len(file.Resources)+len(file.Bases)+len(file.Components))

	deps = append(deps, file.Resources...)
	deps = append(deps, file.Bases...)      // deprecated, still supported
	deps = append(deps, file.Components...) // components

	return deps
}

// GetDependentOverlays returns all overlays that depend on the given base path.
//
// Note for callers: the reverse-lookup index is keyed by every *referenced*
// path, not only by discovered kustomizations, so that a base the change deleted
// still propagates to its overlays. A consequence is that a path which is not a
// kustomization at all — a plain manifest listed under resources — also has an
// entry. Callers must therefore pass a kustomization directory, as the analyzer
// does; passing an arbitrary file path returns whoever references that file,
// which is probably not what you want. Use IsBase or GetNode to check first.
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

	slog.Debug("Finding all dependents", "path", path)

	// Recursive helper function
	var collectDependents func(currentPath string)
	collectDependents = func(currentPath string) {
		currentPath = filepath.Clean(currentPath)

		// Get direct dependents
		if dependents, exists := g.reverseLookup[currentPath]; exists {
			slog.Debug("Found direct dependents",
				"path", currentPath,
				"dependents", dependents)

			for _, dependent := range dependents {
				dependent = filepath.Clean(dependent)

				// Avoid cycles
				if visited[dependent] {
					slog.Debug("Skipping already visited dependent", "path", dependent)
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

	slog.Debug("All dependents found",
		"path", path,
		"total_dependents", len(result))

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
