package builder

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

// BuildResult represents the result of a kustomize build
type BuildResult struct {
	Path string
	// Success is true only for a build that ran and exited 0. A skipped result
	// is neither a success nor a failure, so check Skipped before treating
	// !Success as a build error.
	Success bool
	// Skipped marks a path that was never handed to kustomize because the change
	// removed it. Skipped results are excluded from both the success and the
	// failure counts. See skipReason for exactly what qualifies.
	Skipped    bool
	SkipReason string
	Output     string
	Error      string
	Duration   time.Duration
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

	// A path the change removed is not a build target. Deleting or renaming a
	// kustomize directory leaves its old path in the diff, so it still reaches
	// this point as a candidate; building it would report a bogus failure for a
	// directory the change legitimately removed.
	if reason := skipReason(path); reason != "" {
		slog.Debug("Skipping build", "path", path, "reason", reason)
		return BuildResult{
			Path:       path,
			Success:    false,
			Skipped:    true,
			SkipReason: reason,
			Duration:   time.Since(start),
		}
	}

	args := []string{"build"}
	if enableHelm {
		args = append(args, "--enable-helm")
	}
	args = append(args, path)

	slog.Debug("Starting kustomize build",
		"path", path,
		"enable_helm", enableHelm,
		"args", args)

	cmd := exec.Command("kustomize", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set timeout
	timer := time.AfterFunc(b.timeout, func() {
		slog.Warn("Kustomize build timeout, killing process", "path", path)
		_ = cmd.Process.Kill() // Ignore error, process might have already exited
	})
	defer timer.Stop()

	err := cmd.Run()
	duration := time.Since(start)

	if err != nil {
		slog.Debug("Kustomize build failed",
			"path", path,
			"duration", duration,
			"error", err)
		return BuildResult{
			Path:     path,
			Success:  false,
			Output:   stdout.String(),
			Error:    fmt.Sprintf("%v\n%s", err, stderr.String()),
			Duration: duration,
		}
	}

	slog.Debug("Kustomize build succeeded",
		"path", path,
		"duration", duration)

	return BuildResult{
		Path:     path,
		Success:  true,
		Output:   stdout.String(),
		Error:    "",
		Duration: duration,
	}
}

// skipReason reports why path is not a build target, or "" if it should be
// handed to kustomize.
//
// The check is deliberately narrow. A directory that still holds content but
// has lost its kustomization file is a genuine error and must stay a failure,
// so only paths the change actually removed are skipped.
func skipReason(path string) string {
	entries, err := os.ReadDir(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "removed in this change"
	case err != nil:
		// Not a directory, or unreadable. Let kustomize report the problem.
		return ""
	case len(entries) == 0:
		// git cannot represent an empty directory, so there is nothing here in
		// the commit under test: a fresh checkout would not have this path at
		// all. It only survives in reused workspaces, where moving the last
		// file out of a directory leaves the empty directory behind.
		return "removed in this change (empty directory)"
	}
	return ""
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
