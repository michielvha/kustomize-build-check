// Package integration exercises the full change-detection pipeline
// (git diff -> discovery -> graph -> impact analysis -> build) against real
// git repositories and a real kustomize binary.
//
// These tests are the regression guard for the class of bug where a directory
// that a change deleted or renamed was still handed to kustomize and reported
// as a build failure.
package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/michielvha/kustomize-build-check/internal/analyzer"
	"github.com/michielvha/kustomize-build-check/internal/builder"
	"github.com/michielvha/kustomize-build-check/internal/discovery"
	"github.com/michielvha/kustomize-build-check/internal/git"
	"github.com/michielvha/kustomize-build-check/internal/graph"
	"github.com/michielvha/kustomize-build-check/internal/reporter"
)

const kustomizationHeader = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
`

// repo is a scratch git repository used to stage a "before" and "after" commit.
type repo struct {
	t    *testing.T
	dir  string
	base string // sha of the base commit
}

func newRepo(t *testing.T) *repo {
	t.Helper()

	requireBinary(t, "git")

	// Resolve symlinks so the paths we assert on match the absolute paths the
	// analyzer derives from the working directory (on macOS /var -> /private/var).
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}

	r := &repo{t: t, dir: dir}
	r.git("init", "-q", "-b", "main", ".")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "user.name", "test")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// write creates or overwrites a file relative to the repo root.
func (r *repo) write(relPath, content string) {
	r.t.Helper()

	full := filepath.Join(r.dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatalf("failed to create dir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatalf("failed to write %s: %v", relPath, err)
	}
}

// remove deletes a file or directory relative to the repo root.
func (r *repo) remove(relPath string) {
	r.t.Helper()

	if err := os.RemoveAll(filepath.Join(r.dir, relPath)); err != nil {
		r.t.Fatalf("failed to remove %s: %v", relPath, err)
	}
}

// commitBase records the starting point that the change is diffed against.
func (r *repo) commitBase() {
	r.t.Helper()

	r.git("add", "-A")
	r.git("commit", "-qm", "base")
	r.base = strings.TrimSpace(r.git("rev-parse", "HEAD"))
}

// commitChange records the head commit, i.e. the contents of the pull request.
func (r *repo) commitChange() {
	r.t.Helper()

	r.git("add", "-A")
	r.git("commit", "-qm", "change")
}

// path returns the absolute path of a repo-relative path, in the form the
// pipeline reports it.
func (r *repo) path(relPath string) string {
	return filepath.Join(r.dir, relPath)
}

// run executes the same pipeline as cmd/action and returns the summary.
func (r *repo) run() reporter.Summary {
	r.t.Helper()

	requireBinary(r.t, "kustomize")

	// The analyzer resolves changed files against the working directory.
	r.t.Chdir(r.dir)

	changedFiles, err := git.New().GetChangedFiles(r.base, "HEAD")
	if err != nil {
		r.t.Fatalf("GetChangedFiles failed: %v", err)
	}

	kustomizations, err := discovery.New().FindAll(".")
	if err != nil {
		r.t.Fatalf("FindAll failed: %v", err)
	}

	g := graph.New()
	if err := g.Build(kustomizations); err != nil {
		r.t.Fatalf("graph Build failed: %v", err)
	}

	affected := analyzer.New().GetAffectedKustomizations(changedFiles, g, kustomizations)
	results := builder.New().BuildAll(affected, false)

	return reporter.New().GenerateSummary(results)
}

func requireBinary(t *testing.T, name string) {
	t.Helper()

	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not installed, skipping", name)
	}
}

// resultFor returns the result recorded for an absolute path.
func resultFor(t *testing.T, s reporter.Summary, path string) builder.BuildResult {
	t.Helper()

	for _, res := range s.Results {
		if res.Path == path {
			return res
		}
	}
	t.Fatalf("no result recorded for %s\ngot: %s", path, paths(s))
	return builder.BuildResult{}
}

// assertNoResult fails if the path was validated at all.
func assertNoResult(t *testing.T, s reporter.Summary, path string) {
	t.Helper()

	for _, res := range s.Results {
		if res.Path == path {
			t.Errorf("expected %s not to be a build target, got %+v", path, res)
		}
	}
}

func paths(s reporter.Summary) string {
	var got []string
	for _, res := range s.Results {
		got = append(got, res.Path)
	}
	return strings.Join(got, "\n     ")
}

// overlayWithLocalDir writes an overlay that pulls in a subdirectory as a resource.
func (r *repo) overlayWithLocalDir(env string) {
	r.write("manifests/overlays/app/"+env+"/kustomization.yaml",
		kustomizationHeader+"resources:\n  - s3-synchronisation\n")
	r.write("manifests/overlays/app/"+env+"/s3-synchronisation/kustomization.yaml",
		kustomizationHeader+"resources:\n  - cronjob.yaml\n")
	r.write("manifests/overlays/app/"+env+"/s3-synchronisation/cronjob.yaml",
		"apiVersion: batch/v1\nkind: CronJob\nmetadata:\n  name: s3-sync\nspec:\n  schedule: \"0 * * * *\"\n")
}

// TestConsolidateDuplicatedDirsIntoComponent reproduces the reported failure:
// three byte-identical overlay directories collapsed into one shared component.
//
// Acceptance criterion 1: the PR passes, and the removed directories are
// reported as skipped rather than failed.
func TestConsolidateDuplicatedDirsIntoComponent(t *testing.T) {
	r := newRepo(t)
	for _, env := range []string{"dev", "int", "acc"} {
		r.overlayWithLocalDir(env)
	}
	r.commitBase()

	// The change: hoist the duplicated directory into a shared component.
	r.write("manifests/components/s3-synchronisation/kustomization.yaml",
		"apiVersion: kustomize.config.k8s.io/v1alpha1\nkind: Component\nresources:\n  - cronjob.yaml\n")
	r.write("manifests/components/s3-synchronisation/cronjob.yaml",
		"apiVersion: batch/v1\nkind: CronJob\nmetadata:\n  name: s3-sync\nspec:\n  schedule: \"0 * * * *\"\n")
	for _, env := range []string{"dev", "int", "acc"} {
		r.remove("manifests/overlays/app/" + env + "/s3-synchronisation")
		r.write("manifests/overlays/app/"+env+"/kustomization.yaml",
			kustomizationHeader+"components:\n  - ../../../components/s3-synchronisation\n")
	}
	r.commitChange()

	summary := r.run()

	if summary.Failed != 0 {
		t.Errorf("expected no failures, got %d failed\n     %s", summary.Failed, paths(summary))
	}

	// Every surviving directory must still be validated.
	for _, env := range []string{"dev", "int", "acc"} {
		res := resultFor(t, summary, r.path("manifests/overlays/app/"+env))
		if !res.Success {
			t.Errorf("overlay %s should have built: %s", env, res.Error)
		}
	}

	// Whichever removed directories were attributed to the diff must be skipped,
	// never failed. Git's rename detection consumes one of the three identical
	// source directories as the rename source, so only some of them appear.
	skipped := 0
	for _, env := range []string{"dev", "int", "acc"} {
		removed := r.path("manifests/overlays/app/" + env + "/s3-synchronisation")
		for _, res := range summary.Results {
			if res.Path != removed {
				continue
			}
			skipped++
			if !res.Skipped {
				t.Errorf("removed dir %s should be skipped, got %+v", removed, res)
			}
			if res.Success {
				t.Errorf("removed dir %s must not count as a success", removed)
			}
		}
	}
	if skipped == 0 {
		t.Fatal("expected at least one removed directory to reach the build step; " +
			"the regression this test guards would no longer be covered")
	}
	if summary.Skipped != skipped {
		t.Errorf("expected %d skipped, got %d", skipped, summary.Skipped)
	}

	// Criterion 3: skipped paths are excluded from both counts.
	if summary.Success+summary.Failed+summary.Skipped != summary.Total {
		t.Errorf("counts do not add up: %d + %d + %d != %d",
			summary.Success, summary.Failed, summary.Skipped, summary.Total)
	}
}

// TestDeletedDirectoryIsSkipped covers the simplest form of criterion 1:
// an obsolete environment is removed outright.
func TestDeletedDirectoryIsSkipped(t *testing.T) {
	r := newRepo(t)
	r.write("base/kustomization.yaml", kustomizationHeader+"resources:\n  - deployment.yaml\n")
	r.write("base/deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n")
	r.write("overlays/obsolete/kustomization.yaml", kustomizationHeader+"resources:\n  - ../../base\n")
	r.commitBase()

	r.remove("overlays/obsolete")
	r.commitChange()

	summary := r.run()

	if summary.Failed != 0 {
		t.Errorf("expected no failures, got %d\n     %s", summary.Failed, paths(summary))
	}

	res := resultFor(t, summary, r.path("overlays/obsolete"))
	if !res.Skipped {
		t.Errorf("deleted overlay should be skipped, got %+v", res)
	}
	if res.SkipReason == "" {
		t.Error("skipped result should carry a reason")
	}
	if summary.Success != 0 {
		t.Errorf("a skipped path must not be counted as a success, got %d", summary.Success)
	}
}

// TestRenamedDirectoryValidatesNewPath covers criterion 2.
func TestRenamedDirectoryValidatesNewPath(t *testing.T) {
	r := newRepo(t)
	r.write("base/kustomization.yaml", kustomizationHeader+"resources:\n  - deployment.yaml\n")
	r.write("base/deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n")
	r.write("overlays/staging/kustomization.yaml", kustomizationHeader+"resources:\n  - ../../base\n")
	r.commitBase()

	// Rename overlays/staging -> overlays/preprod.
	r.write("overlays/preprod/kustomization.yaml", kustomizationHeader+"resources:\n  - ../../base\n")
	r.remove("overlays/staging")
	r.commitChange()

	summary := r.run()

	if summary.Failed != 0 {
		t.Errorf("expected no failures, got %d\n     %s", summary.Failed, paths(summary))
	}

	newPath := resultFor(t, summary, r.path("overlays/preprod"))
	if !newPath.Success {
		t.Errorf("renamed destination should build: %s", newPath.Error)
	}

	// The old path is either absent from the diff (rename detection) or skipped,
	// but it must never be reported as a failure.
	for _, res := range summary.Results {
		if res.Path == r.path("overlays/staging") && !res.Skipped {
			t.Errorf("old path should not be built, got %+v", res)
		}
	}
}

// TestModifiedDirectoryStillBuilds is the happy path: an ordinary edit is
// still discovered and validated.
func TestModifiedDirectoryStillBuilds(t *testing.T) {
	r := newRepo(t)
	r.write("base/kustomization.yaml", kustomizationHeader+"resources:\n  - deployment.yaml\n")
	r.write("base/deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n")
	r.write("overlays/dev/kustomization.yaml", kustomizationHeader+"resources:\n  - ../../base\n")
	r.commitBase()

	r.write("base/deployment.yaml",
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n  labels:\n    tier: web\n")
	r.commitChange()

	summary := r.run()

	if summary.Skipped != 0 {
		t.Errorf("nothing was removed, expected 0 skipped, got %d", summary.Skipped)
	}
	if summary.Failed != 0 {
		t.Errorf("expected no failures, got %d\n     %s", summary.Failed, paths(summary))
	}

	// The edited base and the overlay that consumes it are both validated.
	if res := resultFor(t, summary, r.path("base")); !res.Success {
		t.Errorf("base should build: %s", res.Error)
	}
	if res := resultFor(t, summary, r.path("overlays/dev")); !res.Success {
		t.Errorf("dependent overlay should build: %s", res.Error)
	}
}

// TestBrokenKustomizationStillFails covers criterion 4: a directory that still
// exists must not be filtered out by the skip guard.
func TestBrokenKustomizationStillFails(t *testing.T) {
	r := newRepo(t)
	r.write("base/kustomization.yaml", kustomizationHeader+"resources:\n  - deployment.yaml\n")
	r.write("base/deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n")
	r.commitBase()

	// Point at a resource that does not exist.
	r.write("base/kustomization.yaml", kustomizationHeader+"resources:\n  - missing.yaml\n")
	r.commitChange()

	summary := r.run()

	if summary.Failed == 0 {
		t.Errorf("a broken kustomization must still fail, got %+v", summary)
	}
	if summary.Skipped != 0 {
		t.Errorf("an existing directory must never be skipped, got %d skipped", summary.Skipped)
	}
}

// TestDirectoryPresentButKustomizationRemovedFails covers the over-filtering
// guard in criterion 4: the directory survives but has lost its kustomization
// file while a sibling file changed. That is a real error, not a skip.
//
// This is why the guard checks only for existence of the directory rather than
// for the presence of a kustomization file.
func TestDirectoryPresentButKustomizationRemovedFails(t *testing.T) {
	r := newRepo(t)
	r.write("base/kustomization.yaml", kustomizationHeader+"resources:\n  - deployment.yaml\n")
	r.write("base/deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n")
	r.commitBase()

	// Drop the kustomization file but leave the directory and a changed sibling.
	r.remove("base/kustomization.yaml")
	r.write("base/deployment.yaml",
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n  labels:\n    tier: web\n")
	r.commitChange()

	summary := r.run()

	if summary.Skipped != 0 {
		t.Errorf("the directory still exists, it must not be skipped, got %d skipped\n     %s",
			summary.Skipped, paths(summary))
	}
	if summary.Failed == 0 {
		t.Errorf("a directory that lost its kustomization file must surface as an error, got %+v", summary)
	}
}

// TestDeletedResourceStillReferencedFails is the false-pass guard.
//
// Deleting a file that a surviving kustomization still references is a genuine
// breakage. It is detected only because deletions stay in the diff and mark the
// consuming kustomization as affected. Filtering deletions out of the diff (for
// example with --diff-filter=d) would make this change report a green check.
func TestDeletedResourceStillReferencedFails(t *testing.T) {
	r := newRepo(t)
	r.write("base/kustomization.yaml", kustomizationHeader+"resources:\n  - deployment.yaml\n")
	r.write("base/deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n")
	r.commitBase()

	// Remove the resource but leave the reference in place.
	r.remove("base/deployment.yaml")
	r.commitChange()

	summary := r.run()

	assertNoResult(t, summary, r.path("base/deployment.yaml"))
	if summary.Failed == 0 {
		t.Fatalf("deleting a still-referenced resource must fail the check, got %+v", summary)
	}
	if res := resultFor(t, summary, r.path("base")); res.Skipped {
		t.Error("the base directory still exists and must not be skipped")
	}
}

// assertAffected compares the set of validated paths against an exact expected
// set. It asserts **equality**, not containment, so over-matching fails as
// loudly as under-matching — this tool can be wrong in both directions and the
// tests must be able to say so (F-E4).
//
// Paths are given repo-relative and resolved against the fixture root.
func (r *repo) assertAffected(s reporter.Summary, want ...string) {
	r.t.Helper()

	got := make([]string, 0, len(s.Results))
	for _, res := range s.Results {
		got = append(got, res.Path)
	}
	wantAbs := make([]string, 0, len(want))
	for _, w := range want {
		wantAbs = append(wantAbs, r.path(w))
	}
	sort.Strings(got)
	sort.Strings(wantAbs)

	if !slices.Equal(got, wantAbs) {
		r.t.Errorf("validated set mismatch\n  got:  %s\n  want: %s",
			strings.Join(got, "\n        "), strings.Join(wantAbs, "\n        "))
	}
}

// TestCrossDirectoryResourceIsValidatedNotSilentlyIgnored is the end-to-end
// guard for G1 (AC-A1, AC-A2, AC-A3).
//
// Note what is asserted, and why. A cross-directory *file* reference is rejected
// by kustomize's default load restrictor ("security; file ... is not in or below
// ..."), and this action does not pass --load-restrictor LoadRestrictionsNone.
// So the meaningful assertion is not "base builds" — it cannot — but that the
// directory is **validated at all**. Before this phase nothing marked it
// affected, so the run reported "No kustomizations affected by changes" and
// exited 0 while the repo was broken. Now the breakage is surfaced.
//
// The fixture includes a `base-old/` sibling that must NOT be dragged in, which
// is why the assertion is set equality rather than containment.
func TestCrossDirectoryResourceIsValidatedNotSilentlyIgnored(t *testing.T) {
	r := newRepo(t)
	r.write("shared/cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  k: v\n")
	r.write("base/kustomization.yaml", kustomizationHeader+"resources:\n  - ../shared/cm.yaml\n")
	// A sibling whose name shares a prefix with `base`, and its own resource.
	r.write("base-old/cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: old\n")
	r.write("base-old/kustomization.yaml", kustomizationHeader+"resources:\n  - cm.yaml\n")
	r.commitBase()

	r.write("shared/cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  k: CHANGED\n")
	r.commitChange()

	summary := r.run()

	// Exactly base — not base-old, which shares a prefix but references nothing changed.
	r.assertAffected(summary, "base")

	// The run must not be silently green: the directory is validated, and the
	// load-restrictor violation it contains is now reported instead of hidden.
	if summary.Failed == 0 {
		t.Errorf("the referencing directory must be validated and its breakage surfaced, got %+v", summary)
	}
	if res := resultFor(t, summary, r.path("base")); res.Skipped {
		t.Error("base still exists and must not be skipped")
	}
}

// TestDeletedBaseFailsDependentOverlay is the end-to-end guard for G5 (AC-A7).
//
// Deleting a base while an overlay still references it is a genuine breakage.
// Before this phase the removed path was correctly skipped and nothing else was
// marked affected, so the run reported "All builds successful" and exited 0
// against a repo where `kustomize build` fails.
func TestDeletedBaseFailsDependentOverlay(t *testing.T) {
	r := newRepo(t)
	r.write("base/kustomization.yaml", kustomizationHeader+"resources:\n  - deployment.yaml\n")
	r.write("base/deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n")
	r.write("overlays/dev/kustomization.yaml", kustomizationHeader+"resources:\n  - ../../base\n")
	r.commitBase()

	r.remove("base")
	r.commitChange()

	summary := r.run()

	// The removed base is skipped; the overlay that still consumes it fails.
	r.assertAffected(summary, "base", "overlays/dev")

	if res := resultFor(t, summary, r.path("base")); !res.Skipped {
		t.Errorf("the removed base must be skipped, got %+v", res)
	}
	if res := resultFor(t, summary, r.path("overlays/dev")); res.Success || res.Skipped {
		t.Errorf("the dependent overlay must fail, got %+v", res)
	}
	if summary.Failed == 0 {
		t.Fatalf("deleting a referenced base must fail the check, got %+v", summary)
	}
}

// TestDottedBaseDirectoryPropagatesToOverlay is the end-to-end guard for G4
// (AC-B1, AC-B2, AC-B3).
//
// `filepath.Ext("../../bases/v1.2")` is ".2", so the old classifier read this
// directory reference as a file and dropped the base-to-overlay edge. Editing
// the base then propagated to nothing.
func TestDottedBaseDirectoryPropagatesToOverlay(t *testing.T) {
	r := newRepo(t)
	r.write("bases/v1.2/kustomization.yaml", kustomizationHeader+"resources:\n  - cm.yaml\n")
	r.write("bases/v1.2/cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  k: v\n")
	r.write("overlays/dev/kustomization.yaml", kustomizationHeader+"resources:\n  - ../../bases/v1.2\n")
	r.commitBase()

	r.write("bases/v1.2/cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  k: CHANGED\n")
	r.commitChange()

	summary := r.run()

	// The edited base AND the overlay that consumes it.
	r.assertAffected(summary, "bases/v1.2", "overlays/dev")
	if summary.Failed != 0 {
		t.Errorf("expected no failures, got %d\n     %s", summary.Failed, paths(summary))
	}
}

// TestDeletedBareBaseFailsDependentOverlay is the AC-B7 guard.
//
// This is the variant Phase 1 could NOT close. When the deleted base directory
// held nothing but its kustomization.yaml, the only changed path is that file
// itself, so there is nothing *under* the directory for resolved-reference
// matching to catch. It is caught instead by the graph recording the reverse
// edge even where no node was discovered.
func TestDeletedBareBaseFailsDependentOverlay(t *testing.T) {
	r := newRepo(t)
	// base/ holds ONLY kustomization.yaml.
	r.write("base/kustomization.yaml", kustomizationHeader+"resources: []\n")
	r.write("overlays/dev/kustomization.yaml", kustomizationHeader+"resources:\n  - ../../base\n")
	r.commitBase()

	r.remove("base")
	r.commitChange()

	summary := r.run()

	r.assertAffected(summary, "base", "overlays/dev")

	if res := resultFor(t, summary, r.path("base")); !res.Skipped {
		t.Errorf("the removed base must be skipped, got %+v", res)
	}
	if res := resultFor(t, summary, r.path("overlays/dev")); res.Success || res.Skipped {
		t.Errorf("the dependent overlay must fail, got %+v", res)
	}
	if summary.Failed == 0 {
		t.Fatalf("deleting a bare referenced base must fail the check, got %+v", summary)
	}
}
