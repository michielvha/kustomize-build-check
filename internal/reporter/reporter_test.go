package reporter

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michielvha/kustomize-build-check/internal/builder"
)

func sampleResults() []builder.BuildResult {
	return []builder.BuildResult{
		{Path: "/repo/overlays/dev", Success: true},
		{Path: "/repo/overlays/int", Success: true},
		{Path: "/repo/overlays/broken", Success: false, Error: "exit status 1\nError: boom"},
		{Path: "/repo/overlays/removed", Skipped: true, SkipReason: "removed in this change"},
	}
}

// TestGenerateSummaryExcludesSkipped covers acceptance criterion 3: skipped
// paths count towards neither success nor failure.
func TestGenerateSummaryExcludesSkipped(t *testing.T) {
	summary := New().GenerateSummary(sampleResults())

	if summary.Success != 2 {
		t.Errorf("expected 2 successful, got %d", summary.Success)
	}
	if summary.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", summary.Failed)
	}
	if summary.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", summary.Skipped)
	}
	if summary.Success+summary.Failed+summary.Skipped != summary.Total {
		t.Errorf("counts do not add up to total %d: %d/%d/%d",
			summary.Total, summary.Success, summary.Failed, summary.Skipped)
	}
}

// TestSetGitHubOutputsExcludesSkipped checks the values consumers actually read.
func TestSetGitHubOutputsExcludesSkipped(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "github_output")
	t.Setenv("GITHUB_OUTPUT", outputFile)

	if err := New().SetGitHubOutputs(sampleResults()); err != nil {
		t.Fatalf("SetGitHubOutputs failed: %v", err)
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	got := string(data)

	for _, want := range []string{"failed-count=1", "success-count=2", "skipped-count=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

// TestStepSummaryDoesNotListSkippedAsError makes sure a removed directory is not
// rendered in the Build Errors section of the job summary.
func TestStepSummaryDoesNotListSkippedAsError(t *testing.T) {
	summaryFile := filepath.Join(t.TempDir(), "step_summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", summaryFile)

	if err := New().WriteGitHubStepSummary(sampleResults()); err != nil {
		t.Fatalf("WriteGitHubStepSummary failed: %v", err)
	}

	data, err := os.ReadFile(summaryFile)
	if err != nil {
		t.Fatalf("failed to read summary file: %v", err)
	}
	got := string(data)

	_, errorsSection, found := strings.Cut(got, "### ❌ Build Errors")
	if !found {
		t.Fatalf("expected a Build Errors section, got:\n%s", got)
	}
	errorsSection, _, _ = strings.Cut(errorsSection, "### ")

	if strings.Contains(errorsSection, "/repo/overlays/removed") {
		t.Errorf("skipped path must not appear as a build error, got:\n%s", errorsSection)
	}
	if !strings.Contains(errorsSection, "/repo/overlays/broken") {
		t.Errorf("genuinely failed path should appear as a build error, got:\n%s", errorsSection)
	}
	if !strings.Contains(got, "### ⏭️ Skipped") {
		t.Errorf("expected a skipped section, got:\n%s", got)
	}
}

// TestParseIssuesAreNotBuildErrors covers AC-C7: the parse-issue surface is
// additive and must not disturb the counts or the outputs contract.
func TestParseIssuesAreNotBuildErrors(t *testing.T) {
	summaryFile := filepath.Join(t.TempDir(), "step_summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", summaryFile)

	rep := New()
	issues := []ParseIssue{{Path: "/repo/app/kustomization.yaml", Reason: "failed to parse YAML: boom"}}

	if err := rep.WriteGitHubStepSummary(sampleResults()); err != nil {
		t.Fatalf("WriteGitHubStepSummary failed: %v", err)
	}
	if err := rep.AppendParseIssuesToStepSummary(issues); err != nil {
		t.Fatalf("AppendParseIssuesToStepSummary failed: %v", err)
	}

	data, err := os.ReadFile(summaryFile)
	if err != nil {
		t.Fatalf("failed to read summary: %v", err)
	}
	got := string(data)

	if !strings.Contains(got, "### ⚠️ Could not be fully parsed") {
		t.Errorf("expected a parse-issue section, got:\n%s", got)
	}
	// The parse issue must not appear inside the Build Errors section.
	_, errorsSection, found := strings.Cut(got, "### ❌ Build Errors")
	if found {
		errorsSection, _, _ = strings.Cut(errorsSection, "### ")
		if strings.Contains(errorsSection, "/repo/app/kustomization.yaml") {
			t.Errorf("a parse issue must not be rendered as a build error:\n%s", errorsSection)
		}
	}

	// Counts are untouched by parse issues.
	s := rep.GenerateSummary(sampleResults())
	if s.Success != 2 || s.Failed != 1 || s.Skipped != 1 {
		t.Errorf("parse issues must not change the counts, got %+v", s)
	}
}

// TestParseIssuesAddNoOutputKey covers AC-C7's output half: the public outputs
// contract is unchanged by this phase.
func TestParseIssuesAddNoOutputKey(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "github_output")
	t.Setenv("GITHUB_OUTPUT", outputFile)

	if err := New().SetGitHubOutputs(sampleResults()); err != nil {
		t.Fatalf("SetGitHubOutputs failed: %v", err)
	}
	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)

	// The four pre-existing keys are present with unchanged shapes. Asserted as
	// presence, not as "these are the only keys" — shallow-clone-support adds a
	// fifth, and a count assertion here would fail for an unrelated reason.
	for _, want := range []string{"failed-count=1", "success-count=2", "skipped-count=1", "results="} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in outputs, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "parse") {
		t.Errorf("this phase must add no parse-related output key, got:\n%s", got)
	}
}

// TestPrintParseIssues covers the console half of AC-C2, which had no test at
// all: a parse problem must be visible in the run output, not only in the step
// summary (which is absent on a local run) and not only on stderr.
func TestPrintParseIssues(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	stdout := os.Stdout
	os.Stdout = w
	New().PrintParseIssues([]ParseIssue{{
		Path:   "/repo/app/kustomization.yaml",
		Reason: "failed to parse YAML: boom",
	}})
	_ = w.Close()
	os.Stdout = stdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read: %v", err)
	}
	got := buf.String()

	for _, want := range []string{"/repo/app/kustomization.yaml", "failed to parse YAML: boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("console output must contain %q, got:\n%s", want, got)
		}
	}
}

// TestPrintParseIssuesSilentWhenNone keeps the happy path quiet.
func TestPrintParseIssuesSilentWhenNone(t *testing.T) {
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	New().PrintParseIssues(nil)
	_ = w.Close()
	os.Stdout = stdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if buf.Len() != 0 {
		t.Errorf("no parse issues must print nothing, got:\n%s", buf.String())
	}
}
