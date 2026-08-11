package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Analyzer detects changed files between git references
type Analyzer interface {
	GetChangedFiles(baseRef, headRef string) ([]string, error)
}

type analyzer struct{}

// New creates a new Git analyzer
func New() Analyzer {
	return &analyzer{}
}

// GetChangedFiles returns the list of files changed between baseRef and headRef
func (a *analyzer) GetChangedFiles(baseRef, headRef string) ([]string, error) {
	if baseRef == "" {
		baseRef = "HEAD~1"
	}
	if headRef == "" {
		headRef = "HEAD"
	}

	// Deletions are intentionally NOT filtered out here (e.g. with
	// --diff-filter=d). Deleting a file that a surviving kustomization still
	// references is a real breakage, and it is only detected because the deleted
	// path reaches the impact analyzer and marks the consuming kustomization as
	// affected. Filtering deletions at this layer would turn that into a silent
	// pass. Paths that no longer exist are dropped later, at the build step,
	// where the existence of the build target can be checked directly.
	//
	// Rename detection (git's default) already reports only the new path for a
	// renamed file, so renames validate the new location without the old one.
	cmd := exec.Command("git", "diff", "--name-only", baseRef, headRef)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff failed: %w\nStderr: %s", err, stderr.String())
	}

	output := stdout.String()
	if output == "" {
		return []string{}, nil
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	var files []string
	for _, line := range lines {
		if line != "" {
			files = append(files, strings.TrimSpace(line))
		}
	}

	return files, nil
}
