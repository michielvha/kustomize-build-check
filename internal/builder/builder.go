package builder

import (
	"bytes"
	"context"
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

	// TimedOut marks a build killed on the time limit rather than rejected by
	// kustomize. It is the ONLY supported way to detect a timeout: never parse
	// Error, and never infer it from the exit code, which is indistinguishable
	// from an OOM kill.
	//
	// A timed-out build was not validated, so it is a failure, not a third
	// outcome: it counts in Failed and it fails the run.
	TimedOut bool
	// TimeoutLimit is the limit that applied to this build, set on every result
	// the exec path produces whether it timed out or not. Zero on a skipped
	// result, which never ran.
	TimeoutLimit time.Duration
}

// Builder executes kustomize builds
type Builder interface {
	Build(path string, enableHelm bool) BuildResult
	BuildAll(paths []string, enableHelm bool) []BuildResult
}

// defaultWaitGrace bounds how long Run may block *after* the deadline kills the
// child, while a grandchild still holds the inherited output pipe.
//
// This is what makes the limit a real wall-clock bound. stdout and stderr are
// bytes.Buffers, so Wait blocks until every writer closes the pipe — and
// --enable-helm makes kustomize exec helm, so a grandchild holding it is the
// normal case, not a corner case. Measured against a 300ms deadline: ~30s with
// no WaitDelay, ~0.8s at 500ms.
//
// It is a package-level default held in an unexported field rather than a const
// so the tests can assert the mechanism without paying five seconds per case. It
// is deliberately not reachable from outside this package: no input, no env var,
// no exported field or parameter.
const defaultWaitGrace = 5 * time.Second

type builder struct {
	timeout time.Duration
	grace   time.Duration
	command string
}

// New creates a Builder with the default 2-minute per-build timeout.
func New() Builder {
	return NewWithTimeout(2 * time.Minute)
}

// NewWithTimeout creates a Builder with an explicit per-build timeout.
//
// The limit is per build, not per run: a slow directory does not consume the
// budget of the ones after it.
func NewWithTimeout(timeout time.Duration) Builder {
	return &builder{
		timeout: timeout,
		grace:   defaultWaitGrace,
		command: "kustomize",
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

	// A context deadline rather than a hand-rolled kill timer.
	//
	// The previous time.AfterFunc was armed before the process started, so a
	// short limit dereferenced a nil cmd.Process inside a goroutine with no
	// recover, taking the whole action down. CommandContext removes that by
	// construction, and ctx.Err() is the only honest way to tell a timeout from
	// an ordinary failure: the exit status of a killed process is
	// indistinguishable from an OOM kill.
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, b.command, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Bound how long Wait may block after the kill; see defaultWaitGrace.
	cmd.WaitDelay = b.grace

	err := cmd.Run()
	duration := time.Since(start)

	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		slog.Warn("Kustomize build timed out",
			"path", path,
			"limit", b.timeout,
			"duration", duration)
		return BuildResult{
			Path:    path,
			Success: false,
			Output:  stdout.String(),
			Error: fmt.Sprintf(
				"timed out after %s (limit %s); raise the build-timeout input if this build is legitimately slow\n%s",
				duration.Round(time.Millisecond), b.timeout, stderr.String()),
			Duration:     duration,
			TimedOut:     true,
			TimeoutLimit: b.timeout,
		}
	}

	if err != nil {
		slog.Debug("Kustomize build failed",
			"path", path,
			"duration", duration,
			"error", err)
		return BuildResult{
			Path:         path,
			Success:      false,
			Output:       stdout.String(),
			Error:        fmt.Sprintf("%v\n%s", err, stderr.String()),
			Duration:     duration,
			TimeoutLimit: b.timeout,
		}
	}

	slog.Debug("Kustomize build succeeded",
		"path", path,
		"duration", duration)

	return BuildResult{
		Path:         path,
		Success:      true,
		Output:       stdout.String(),
		Error:        "",
		Duration:     duration,
		TimeoutLimit: b.timeout,
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
