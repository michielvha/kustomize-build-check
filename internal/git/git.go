package git

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// BaseState classifies whether a base ref can actually be diffed against.
type BaseState string

const (
	// BaseResolved means the ref names a commit present in the local object store.
	BaseResolved BaseState = "resolved"
	// BaseUnresolvableShallow means the ref does not resolve and the repository
	// is shallow, so the commit was almost certainly truncated away. This is
	// what actions/checkout's default fetch-depth of 1 produces.
	BaseUnresolvableShallow BaseState = "unresolvable-shallow"
	// BaseUnresolvableNotShallow means the ref does not resolve in a complete
	// repository: a typo, a deleted branch, or an expression that evaluated to
	// something unexpected.
	BaseUnresolvableNotShallow BaseState = "unresolvable-not-shallow"
	// BaseProbeFailed means the probes themselves could not run: no git on PATH,
	// not a repository, or an exec failure.
	BaseProbeFailed BaseState = "probe-failed"
)

// BaseStatus is the outcome of the preflight, as a classification rather than an
// error. The caller decides what to do about it; this only reports what is true.
type BaseStatus struct {
	Ref    string // the effective ref that was probed
	State  BaseState
	Detail string // for the two failure states, the underlying git message
}

// Analyzer detects changed files between git references
type Analyzer interface {
	GetChangedFiles(baseRef, headRef string) ([]string, error)
	ResolveBase(baseRef string) BaseStatus
}

// EffectiveBaseRef applies the empty-ref default.
//
// This is the single implementation of that default. Duplicating the literal
// would let the ref that gets probed drift from the ref that gets diffed, which
// would make the whole preflight lie.
func EffectiveBaseRef(baseRef string) string {
	if baseRef == "" {
		return "HEAD~1"
	}
	return baseRef
}

// ResolveBase reports whether the base ref can be diffed against, and if not why.
//
// It is deliberately read-only: no fetch, no write to .git, no working-tree
// mutation. The action runs as a non-root user against a workspace owned by
// someone else, and a preflight that mutated state would be a surprising thing
// for a check to do.
func (a *analyzer) ResolveBase(baseRef string) BaseStatus {
	ref := EffectiveBaseRef(baseRef)
	status := BaseStatus{Ref: ref}

	// Probe 1: does the ref name a commit we actually have?
	// --quiet suppresses stderr, so this adds no noise on the happy path.
	out, err := runGit("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	switch {
	case err == nil:
		status.State = BaseResolved
		return status
	case exitCode(err) != 1:
		// Not "unresolvable" — the probe itself could not run.
		status.State = BaseProbeFailed
		status.Detail = strings.TrimSpace(out)
		if status.Detail == "" {
			status.Detail = err.Error()
		}
		return status
	}

	// Probe 2: unresolvable — but is that because the history was truncated?
	// This is the only thing that separates a shallow clone from a typo; both
	// make probe 1 exit 1.
	shallow, err := runGit("rev-parse", "--is-shallow-repository")
	switch strings.TrimSpace(shallow) {
	case "true":
		status.State = BaseUnresolvableShallow
	case "false":
		status.State = BaseUnresolvableNotShallow
	default:
		status.State = BaseProbeFailed
		status.Detail = strings.TrimSpace(shallow)
		if status.Detail == "" && err != nil {
			status.Detail = err.Error()
		}
	}
	return status
}

// runGit runs a read-only git command and returns its combined output.
func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// exitCode returns the process exit code, or -1 when the command did not run at
// all (git missing, exec failure), which is what distinguishes "the ref is bad"
// from "the probe is broken".
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

type analyzer struct{}

// New creates a new Git analyzer
func New() Analyzer {
	return &analyzer{}
}

// GetChangedFiles returns the list of files changed between baseRef and headRef
func (a *analyzer) GetChangedFiles(baseRef, headRef string) ([]string, error) {
	baseRef = EffectiveBaseRef(baseRef)
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
