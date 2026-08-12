package reporter

import (
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
