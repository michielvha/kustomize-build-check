package builder

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBuildSkipsMissingDirectory is the unit-level guard for the reported bug:
// a directory removed by the change must be skipped, not reported as a failure.
func TestBuildSkipsMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "overlays", "removed")

	result := New().Build(missing, false)

	if !result.Skipped {
		t.Errorf("expected a skipped result, got %+v", result)
	}
	if result.Success {
		t.Error("a skipped path must not be reported as a success")
	}
	if result.SkipReason == "" {
		t.Error("a skipped result should carry a reason")
	}
	if result.Error != "" {
		t.Errorf("a skipped path must not carry a build error, got %q", result.Error)
	}
}

// TestBuildSkipsEmptyDirectory covers the leftover directory that moving the
// last file out of a kustomize directory leaves behind in a reused workspace.
// git cannot represent an empty directory, so there is nothing to validate.
func TestBuildSkipsEmptyDirectory(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "moved-away")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	result := New().Build(empty, false)

	if !result.Skipped {
		t.Errorf("expected an empty directory to be skipped, got %+v", result)
	}
	if result.Success {
		t.Error("a skipped path must not be reported as a success")
	}
}

// TestBuildDoesNotSkipExistingDirectoryWithoutKustomization guards against
// over-filtering. A directory that still exists but has lost its kustomization
// file is a genuine error and must be handed to kustomize, not skipped.
func TestBuildDoesNotSkipExistingDirectoryWithoutKustomization(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deployment.yaml"), []byte("kind: Deployment\n"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	result := New().Build(dir, false)

	if result.Skipped {
		t.Errorf("an existing directory must never be skipped, got %+v", result)
	}
}

// The timeout path needs a command whose behaviour we control: sleeping, or
// spawning a grandchild that outlives it and holds the output pipe. Rather than
// depend on a real slow `kustomize build`, the test binary re-execs itself as
// that command.
//
// The gate is an environment variable checked in TestMain. A naive
// os.Args[0] re-exec would run the whole suite recursively, because Build
// constructs its own argv and cannot prepend -test.run.
const fakeCommandEnv = "KBC_FAKE_COMMAND"

func TestMain(m *testing.M) {
	switch os.Getenv(fakeCommandEnv) {
	case "":
		os.Exit(m.Run())

	case "sleep":
		// Outlives any timeout the tests set.
		time.Sleep(30 * time.Second)
		os.Exit(0)

	case "grandchild":
		// Exits immediately, but leaves a descendant holding the inherited
		// stdout pipe. Without WaitDelay, Run blocks until that descendant
		// exits — which is what made the limit not a real bound.
		cmd := exec.Command("sh", "-c", "sleep 30")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Start()
		os.Exit(0)

	case "succeed":
		os.Stdout.WriteString("ok\n")
		os.Exit(0)

	case "fail":
		os.Stderr.WriteString("kustomize said no\n")
		os.Exit(1)
	}
	os.Exit(0)
}

// fakeBuilder returns a builder whose command is this test binary re-execing
// itself in the given mode, plus a directory that will not be skipped.
func fakeBuilder(t *testing.T, mode string, timeout, grace time.Duration) (Builder, string) {
	t.Helper()

	dir := t.TempDir()
	// skipReason skips an empty directory, so the fixture must contain a file.
	if err := os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte("resources: []\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(fakeCommandEnv, mode)

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	return &builder{timeout: timeout, grace: grace, command: self}, dir
}

// TestBuildTimeoutIsClassifiedAsFailure covers the core contract: a timeout is a
// failure carrying a machine-readable cause, not a third outcome.
func TestBuildTimeoutIsClassifiedAsFailure(t *testing.T) {
	b, dir := fakeBuilder(t, "sleep", 100*time.Millisecond, 200*time.Millisecond)

	res := b.Build(dir, false)

	if !res.TimedOut {
		t.Errorf("expected TimedOut, got %+v", res)
	}
	if res.Success {
		t.Error("a timed-out build was never validated and must not be a success")
	}
	if res.Skipped {
		t.Error("a timeout is not a skip: the path exists and was attempted")
	}
	if res.TimeoutLimit != 100*time.Millisecond {
		t.Errorf("TimeoutLimit = %s, want the configured limit", res.TimeoutLimit)
	}
	if !strings.Contains(res.Error, "build-timeout") {
		t.Errorf("the error must name the input that changes the limit, got %q", res.Error)
	}
}

// TestBuildTimeoutIsBoundedByWaitDelay is the B-8 guard: without WaitDelay, Run
// blocks until a grandchild releases the pipe, so the "limit" is not a bound.
func TestBuildTimeoutIsBoundedByWaitDelay(t *testing.T) {
	limit := 100 * time.Millisecond
	grace := 200 * time.Millisecond
	b, dir := fakeBuilder(t, "grandchild", limit, grace)

	start := time.Now()
	res := b.Build(dir, false)
	elapsed := time.Since(start)

	// Generous slack for CI, but far below the 30s the grandchild sleeps.
	if elapsed > 5*time.Second {
		t.Errorf("Run was not bounded: took %s against a %s limit", elapsed, limit)
	}
	if !res.TimedOut {
		t.Errorf("expected TimedOut, got %+v", res)
	}
}

// TestOrdinaryFailureIsNotATimeout guards the discriminator: a build kustomize
// rejected must not be reported as slow.
func TestOrdinaryFailureIsNotATimeout(t *testing.T) {
	b, dir := fakeBuilder(t, "fail", time.Minute, defaultWaitGrace)

	res := b.Build(dir, false)

	if res.TimedOut {
		t.Errorf("an ordinary failure must not be flagged as a timeout, got %+v", res)
	}
	if res.Success {
		t.Error("expected a failure")
	}
	if !strings.Contains(res.Error, "kustomize said no") {
		t.Errorf("stderr must survive into the error, got %q", res.Error)
	}
}

// TestSuccessCarriesTheLimit covers F-05: TimeoutLimit is set on every result the
// exec path produces, so a consumer can always see what limit applied.
func TestSuccessCarriesTheLimit(t *testing.T) {
	b, dir := fakeBuilder(t, "succeed", 42*time.Second, defaultWaitGrace)

	res := b.Build(dir, false)

	if !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}
	if res.TimeoutLimit != 42*time.Second {
		t.Errorf("TimeoutLimit = %s, want it set on a successful result too", res.TimeoutLimit)
	}
}

// TestSkippedResultHasNoLimit covers the other half of F-05: a skipped path never
// ran, so no limit applied to it.
func TestSkippedResultHasNoLimit(t *testing.T) {
	res := New().Build(filepath.Join(t.TempDir(), "gone"), false)

	if !res.Skipped {
		t.Fatalf("expected a skip, got %+v", res)
	}
	if res.TimeoutLimit != 0 {
		t.Errorf("a skipped path never ran, so TimeoutLimit must be zero, got %s", res.TimeoutLimit)
	}
}

// TestConstructorsSetTheProductionGrace is the AC-22 guard. Moving the grace out
// of a const into a field means nothing fails if a constructor forgets to set
// it — and a zero WaitDelay silently restores the unbounded Run in production
// while every other test stays green.
func TestConstructorsSetTheProductionGrace(t *testing.T) {
	for name, b := range map[string]Builder{
		"New":            New(),
		"NewWithTimeout": NewWithTimeout(time.Minute),
	} {
		impl, ok := b.(*builder)
		if !ok {
			t.Fatalf("%s: unexpected type %T", name, b)
		}
		if impl.grace != defaultWaitGrace {
			t.Errorf("%s: grace = %s, want %s", name, impl.grace, defaultWaitGrace)
		}
		if impl.command != "kustomize" {
			t.Errorf("%s: command = %q, want kustomize", name, impl.command)
		}
	}
	if defaultWaitGrace != 5*time.Second {
		t.Errorf("the production grace must be 5s, got %s", defaultWaitGrace)
	}
}

// TestTimeoutIsPerBuildNotPerRun covers F-11: one slow directory must not consume
// the budget of the ones after it.
func TestTimeoutIsPerBuildNotPerRun(t *testing.T) {
	b, dir := fakeBuilder(t, "sleep", 100*time.Millisecond, 200*time.Millisecond)

	results := b.BuildAll([]string{dir, dir, dir}, false)

	if len(results) != 3 {
		t.Fatalf("every path must be attempted, got %d results", len(results))
	}
	for i, res := range results {
		if !res.TimedOut {
			t.Errorf("result %d: expected a timeout, got %+v", i, res)
		}
	}
}
