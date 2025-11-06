package reporter

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/michielvha/kustomize-build-check/internal/builder"
)

// Summary contains aggregated build results
type Summary struct {
	Total   int
	Success int
	Failed  int
	Results []builder.BuildResult
}

// Reporter formats and outputs build results
type Reporter interface {
	GenerateSummary(results []builder.BuildResult) Summary
	PrintResults(results []builder.BuildResult)
	SetGitHubOutputs(results []builder.BuildResult) error
}

type reporter struct{}

// New creates a new Reporter
func New() Reporter {
	return &reporter{}
}

// GenerateSummary creates a summary from build results
func (r *reporter) GenerateSummary(results []builder.BuildResult) Summary {
	summary := Summary{
		Total:   len(results),
		Results: results,
	}

	for _, result := range results {
		if result.Success {
			summary.Success++
		} else {
			summary.Failed++
		}
	}

	return summary
}

// PrintResults outputs results to console with formatting
func (r *reporter) PrintResults(results []builder.BuildResult) {
	if len(results) == 0 {
		fmt.Println("✓ No kustomizations need testing")
		return
	}

	fmt.Println("\nKustomize Build Results:")
	fmt.Println(strings.Repeat("=", 80))

	for _, result := range results {
		if result.Success {
			fmt.Printf("✅ %s - Build successful (%.2fs)\n", result.Path, result.Duration.Seconds())
		} else {
			fmt.Printf("❌ %s - Build failed (%.2fs)\n", result.Path, result.Duration.Seconds())
			if result.Error != "" {
				// Print first few lines of error
				errorLines := strings.Split(result.Error, "\n")
				for i, line := range errorLines {
					if i >= 5 {
						fmt.Println("   ...")
						break
					}
					if line != "" {
						fmt.Printf("   %s\n", line)
					}
				}
			}
		}
	}

	summary := r.GenerateSummary(results)
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("\nSummary: %d total, %d successful, %d failed\n",
		summary.Total, summary.Success, summary.Failed)
}

// SetGitHubOutputs sets GitHub Actions output variables
func (r *reporter) SetGitHubOutputs(results []builder.BuildResult) error {
	summary := r.GenerateSummary(results)

	// Get GitHub output file path
	outputFile := os.Getenv("GITHUB_OUTPUT")
	if outputFile == "" {
		// Not running in GitHub Actions, skip
		return nil
	}

	f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open GITHUB_OUTPUT: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close GITHUB_OUTPUT: %w", closeErr)
		}
	}()

	// Convert results to JSON
	resultsJSON, err := json.Marshal(results)
	if err != nil {
		return fmt.Errorf("failed to marshal results: %w", err)
	}

	// Write outputs
	outputs := []string{
		fmt.Sprintf("failed-count=%d", summary.Failed),
		fmt.Sprintf("success-count=%d", summary.Success),
		fmt.Sprintf("results=%s", resultsJSON),
	}

	for _, output := range outputs {
		if _, err := f.WriteString(output + "\n"); err != nil {
			return fmt.Errorf("failed to write output: %w", err)
		}
	}

	return nil
}
