package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// run executes a command in dir and fails the test on error.
func run(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newOrigin builds a real repository with two commits and returns its resolved
// path plus the sha of the first commit.
//
// Symlinks are resolved because on macOS t.TempDir() hands back /var/... while
// git reports /private/var/..., and a clone from a `file://` URL needs the real
// path.
func newOrigin(t *testing.T) (dir, firstSHA string) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	run(t, dir, "git", "init", "-q", "-b", "main", ".")
	run(t, dir, "git", "config", "user.email", "t@example.com")
	run(t, dir, "git", "config", "user.name", "t")

	if err := os.WriteFile(filepath.Join(dir, "f.yaml"), []byte("a\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-qm", "one")
	firstSHA = run(t, dir, "git", "rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(dir, "f.yaml"), []byte("b\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-qm", "two")

	return dir, firstSHA
}

// shallowClone makes a depth-1 clone. The `file://` form matters: cloning from a
// plain path silently ignores --depth and you get a full repository, which would
// make the shallow tests pass for the wrong reason.
func shallowClone(t *testing.T, origin string) string {
	t.Helper()

	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	dst := filepath.Join(parent, "shallow")
	run(t, parent, "git", "clone", "-q", "--depth", "1", "file://"+origin, dst)

	// Guard the fixture itself: if this is not shallow, the test below proves
	// nothing.
	if got := run(t, dst, "git", "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Fatalf("fixture is not shallow (--depth was ignored): %s", got)
	}
	return dst
}

func TestEffectiveBaseRef(t *testing.T) {
	if got := EffectiveBaseRef(""); got != "HEAD~1" {
		t.Errorf("empty ref must default to HEAD~1, got %q", got)
	}
	if got := EffectiveBaseRef("abc123"); got != "abc123" {
		t.Errorf("an explicit ref must pass through unchanged, got %q", got)
	}
}

func TestResolveBase(t *testing.T) {
	origin, firstSHA := newOrigin(t)

	t.Run("resolved, explicit sha", func(t *testing.T) {
		t.Chdir(origin)
		got := New().ResolveBase(firstSHA)
		if got.State != BaseResolved {
			t.Errorf("state = %q, want %q (detail: %s)", got.State, BaseResolved, got.Detail)
		}
		if got.Ref != firstSHA {
			t.Errorf("ref = %q, want %q", got.Ref, firstSHA)
		}
	})

	t.Run("resolved, default HEAD~1", func(t *testing.T) {
		t.Chdir(origin)
		got := New().ResolveBase("")
		if got.State != BaseResolved {
			t.Errorf("state = %q, want %q", got.State, BaseResolved)
		}
		if got.Ref != "HEAD~1" {
			t.Errorf("ref = %q, want the effective default", got.Ref)
		}
	})

	t.Run("unresolvable, not shallow: a ref that does not exist", func(t *testing.T) {
		t.Chdir(origin)
		got := New().ResolveBase("no-such-branch")
		if got.State != BaseUnresolvableNotShallow {
			t.Errorf("state = %q, want %q", got.State, BaseUnresolvableNotShallow)
		}
	})

	t.Run("unresolvable, shallow: the base was truncated away", func(t *testing.T) {
		t.Chdir(shallowClone(t, origin))
		got := New().ResolveBase(firstSHA)
		if got.State != BaseUnresolvableShallow {
			t.Errorf("state = %q, want %q", got.State, BaseUnresolvableShallow)
		}
	})

	t.Run("unresolvable, shallow: HEAD~1 does not exist either", func(t *testing.T) {
		// The default path fails on a depth-1 clone too, which is why the
		// diagnostic cannot assume an explicit base-ref was given.
		t.Chdir(shallowClone(t, origin))
		got := New().ResolveBase("")
		if got.State != BaseUnresolvableShallow {
			t.Errorf("state = %q, want %q", got.State, BaseUnresolvableShallow)
		}
	})

	t.Run("probe failed: not a git repository", func(t *testing.T) {
		t.Chdir(t.TempDir())
		got := New().ResolveBase("HEAD~1")
		if got.State != BaseProbeFailed {
			t.Errorf("state = %q, want %q (detail: %s)", got.State, BaseProbeFailed, got.Detail)
		}
		if got.Detail == "" {
			t.Error("a failed probe must carry the underlying git message")
		}
	})
}

// TestResolveBaseIsReadOnly pins F-05: the preflight must not fetch, write to
// .git, or touch the working tree. A check that mutated the repo it is checking
// would be a surprising thing to run in CI.
func TestResolveBaseIsReadOnly(t *testing.T) {
	origin, firstSHA := newOrigin(t)
	dir := shallowClone(t, origin)
	t.Chdir(dir)

	before := run(t, dir, "git", "rev-parse", "HEAD")
	beforeCount := run(t, dir, "git", "rev-list", "--count", "HEAD")

	_ = New().ResolveBase(firstSHA)

	if after := run(t, dir, "git", "rev-parse", "HEAD"); after != before {
		t.Errorf("HEAD moved: %s -> %s", before, after)
	}
	if after := run(t, dir, "git", "rev-list", "--count", "HEAD"); after != beforeCount {
		t.Errorf("history was deepened: %s -> %s commits", beforeCount, after)
	}
	if status := run(t, dir, "git", "status", "--porcelain"); status != "" {
		t.Errorf("working tree was modified:\n%s", status)
	}
}

// TestGetChangedFilesUnchanged guards the contract the rest of the pipeline
// depends on: the diff still reports repo-relative paths and still retains
// deletions. Filtering deletions here would hide a resource deleted while still
// referenced.
func TestGetChangedFilesUnchanged(t *testing.T) {
	origin, firstSHA := newOrigin(t)
	t.Chdir(origin)

	// Delete a file, so the deletion has to survive into the result.
	run(t, origin, "git", "rm", "-q", "f.yaml")
	run(t, origin, "git", "commit", "-qm", "delete")

	files, err := New().GetChangedFiles(firstSHA, "HEAD")
	if err != nil {
		t.Fatalf("GetChangedFiles: %v", err)
	}
	if len(files) != 1 || files[0] != "f.yaml" {
		t.Errorf("a deletion must stay in the diff as a repo-relative path, got %v", files)
	}
}
