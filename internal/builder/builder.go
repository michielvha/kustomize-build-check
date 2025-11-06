package builder

import (
	"bytes"
	"fmt"
	"os/exec"
	"time"
)

// BuildResult represents the result of a kustomize build
type BuildResult struct {
	Path     string
	Success  bool
	Output   string
	Error    string
	Duration time.Duration
}

// Builder executes kustomize builds
type Builder interface {
	Build(path string, enableHelm bool) BuildResult
	BuildAll(paths []string, enableHelm bool) []BuildResult
}

type builder struct {
	timeout time.Duration
}

// New creates a new Builder with default 2-minute timeout
func New() Builder {
	return &builder{
		timeout: 2 * time.Minute,
	}
}

// Build executes a single kustomize build
func (b *builder) Build(path string, enableHelm bool) BuildResult {
	start := time.Now()

	args := []string{"build"}
	if enableHelm {
		args = append(args, "--enable-helm")
	}
	args = append(args, path)

	cmd := exec.Command("kustomize", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set timeout
	timer := time.AfterFunc(b.timeout, func() {
		cmd.Process.Kill()
	})
	defer timer.Stop()

	err := cmd.Run()
	duration := time.Since(start)

	if err != nil {
		return BuildResult{
			Path:     path,
			Success:  false,
			Output:   stdout.String(),
			Error:    fmt.Sprintf("%v\n%s", err, stderr.String()),
			Duration: duration,
		}
	}

	return BuildResult{
		Path:     path,
		Success:  true,
		Output:   stdout.String(),
		Error:    "",
		Duration: duration,
	}
}

// BuildAll executes builds for all paths
func (b *builder) BuildAll(paths []string, enableHelm bool) []BuildResult {
	results := make([]BuildResult, 0, len(paths))

	for _, path := range paths {
		result := b.Build(path, enableHelm)
		results = append(results, result)
	}

	return results
}
