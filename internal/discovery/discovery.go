package discovery

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// KustomizeFile represents a parsed kustomization file
type KustomizeFile struct {
	Path       string   // Absolute path to kustomization.yaml
	Dir        string   // Directory containing the file
	Resources  []string // Relative paths referenced
	Bases      []string // Deprecated bases field
	Components []string // Component paths
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
			if err != nil {
				// Log warning but continue
				fmt.Fprintf(os.Stderr, "Warning: failed to parse %s: %v\n", path, err)
				return nil
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

// ParseKustomization parses a kustomization file
func (d *discoverer) ParseKustomization(path string) (*KustomizeFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var content struct {
		Resources  []string `yaml:"resources"`
		Bases      []string `yaml:"bases"`
		Components []string `yaml:"components"`
	}

	if err := yaml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	return &KustomizeFile{
		Path:       absPath,
		Dir:        filepath.Dir(absPath),
		Resources:  content.Resources,
		Bases:      content.Bases,
		Components: content.Components,
	}, nil
}

// isKustomizationFile checks if the filename is a kustomization file
func isKustomizationFile(name string) bool {
	return name == "kustomization.yaml" ||
		name == "kustomization.yml" ||
		name == "Kustomization"
}
