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
