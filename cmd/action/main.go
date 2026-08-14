package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/michielvha/kustomize-build-check/internal/analyzer"
	"github.com/michielvha/kustomize-build-check/internal/builder"
	"github.com/michielvha/kustomize-build-check/internal/discovery"
	"github.com/michielvha/kustomize-build-check/internal/git"
	"github.com/michielvha/kustomize-build-check/internal/graph"
	"github.com/michielvha/kustomize-build-check/internal/reporter"
)

func main() {
	// Configure logging based on LOG_LEVEL environment variable
	// Supported values: DEBUG, INFO, WARN, ERROR (default: INFO)
	setupLogging()

	fmt.Println("🔍 Kustomize Build Check")
	fmt.Println()

	// Read inputs from environment (GitHub Actions sets INPUT_* vars)
	baseRef := getEnv("INPUT_BASE-REF", "")
	enableHelm := getEnv("INPUT_ENABLE-HELM", "true") == "true"
	failOnError := getEnv("INPUT_FAIL-ON-ERROR", "true") == "true"
	rootDir := getEnv("INPUT_ROOT-DIR", ".")
	onUnresolvableBase := getEnv("INPUT_ON-UNRESOLVABLE-BASE", "validate-all")

	// Parse the timeout first: it is a pure parse with no I/O, so a config typo
	// should not first pay for git work before being told it is a typo.
	buildTimeout, err := parseBuildTimeout(getEnv("INPUT_BUILD-TIMEOUT", "2m"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// A config typo is a pure parse with no I/O, so validate it before anything
	// that costs work.
	if onUnresolvableBase != "validate-all" && onUnresolvableBase != "fail" {
		fmt.Fprintf(os.Stderr,
			"Warning: on-unresolvable-base=%q is not recognised; using \"validate-all\". Valid values: validate-all, fail\n",
			onUnresolvableBase)
		onUnresolvableBase = "validate-all"
	}

	// 1. Detect changed files
	fmt.Println("📝 Detecting changed files...")
	gitAnalyzer := git.New()

	// Preflight the base ref first. Diffing against a commit the repository does
	// not have fails with a raw `fatal: bad object`, which tells the user
	// nothing about what to do. Classifying it first lets us say something
	// actionable, and lets us degrade instead of dying.
	base := gitAnalyzer.ResolveBase(baseRef)
	fullScan := false

	var changedFiles []string
	if base.State == git.BaseResolved {
		var err error
		changedFiles, err = gitAnalyzer.GetChangedFiles(baseRef, "HEAD")
		if err != nil {
			// The ref resolved but the diff still failed: nothing here can
			// explain that better than git already has.
			fmt.Fprintf(os.Stderr, "Error detecting changes: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("   Found %d changed files\n", len(changedFiles))
	} else {
		printBaseDiagnostic(base)
		if onUnresolvableBase == "fail" {
			// Matches today's behaviour, but with a message that names the cause
			// and the remedy. No outputs are written, because the run aborted
			// before determining what to build and so took neither mode.
			fmt.Println("\n❌ Cannot determine what changed, and on-unresolvable-base=fail")
			os.Exit(1)
		}
		fullScan = true
		fmt.Println("   Falling back to validating every discovered kustomization.")
	}

	runInfo := reporter.RunInfo{Mode: "diff"}
	if fullScan {
		runInfo = reporter.RunInfo{
			Mode:   "full-scan",
			Reason: baseReason(base),
		}
	}

	// 2. Discover all kustomizations
	fmt.Println("\n🔎 Discovering kustomization files...")
	disc := discovery.New()
	kustomizations, err := disc.FindAll(rootDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering kustomizations: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   Found %d kustomization files\n", len(kustomizations))

	// Collect anything whose references could not be determined. These are
	// validated regardless (the analyzer marks them always-affected); this is
	// how the reason reaches the user instead of dying on stderr.
	parseIssues := collectParseIssues(kustomizations)

	// 3. Build dependency graph
	fmt.Println("\n🕸️  Building dependency graph...")
	g := graph.New()
	if err := g.Build(kustomizations); err != nil {
		fmt.Fprintf(os.Stderr, "Error building graph: %v\n", err)
		os.Exit(1)
	}

	// 4. Analyze impact
	fmt.Println("\n📊 Analyzing impact...")
	var affectedPaths []string
	if fullScan {
		// Every discovered kustomization. This is a strict superset of any
		// diff-derived set, which is the whole reason degrading is safe: it can
		// cost time, but it cannot hide a breakage the diff would have caught.
		affectedPaths = allDirs(kustomizations)
		fmt.Printf("   Full scan: %d kustomization(s) to validate\n", len(affectedPaths))
	} else {
		impactAnalyzer := analyzer.New()
		affectedPaths = impactAnalyzer.GetAffectedKustomizations(changedFiles, g, kustomizations)
	}

	if len(affectedPaths) == 0 {
		fmt.Println("   No kustomizations affected by changes")
		// Even if no paths affected, we should report 0 builds
		rep := reporter.NewWithRunInfo(runInfo)
		rep.PrintParseIssues(parseIssues)
		if err := rep.AppendParseIssuesToStepSummary(parseIssues); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to write parse issues: %v\n", err)
		}
		if err := rep.WriteGitHubStepSummary(nil); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to write GitHub step summary: %v\n", err)
		}
		if err := rep.SetGitHubOutputs(nil); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to set GitHub outputs: %v\n", err)
		}

		fmt.Println("\n✅ All checks passed")
		os.Exit(0)
	}

	fmt.Printf("   %d kustomization(s) need testing:\n", len(affectedPaths))
	for _, path := range affectedPaths {
		fmt.Printf("     - %s\n", path)
	}

	// 5. Build affected kustomizations
	fmt.Println("\n🔨 Running kustomize build...")
	bldr := builder.NewWithTimeout(buildTimeout)
	results := bldr.BuildAll(affectedPaths, enableHelm)

	// 6. Report results
	rep := reporter.NewWithRunInfo(runInfo)
	rep.PrintResults(results)
	rep.PrintParseIssues(parseIssues)
	if err := rep.AppendParseIssuesToStepSummary(parseIssues); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write parse issues: %v\n", err)
	}

	// Set GitHub Actions outputs
	if err := rep.SetGitHubOutputs(results); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to set GitHub outputs: %v\n", err)
	}

	// Write GitHub Step Summary
	if err := rep.WriteGitHubStepSummary(results); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write GitHub step summary: %v\n", err)
	}

	// Determine exit code. Skipped paths are not failures: they are directories
	// the change removed, so there is nothing left to validate.
	summary := rep.GenerateSummary(results)
	if failOnError && summary.Failed > 0 {
		fmt.Println("\n❌ Some builds failed")
		os.Exit(1)
	}

	if summary.Skipped > 0 {
		fmt.Printf("\n✅ All builds successful (%d skipped)\n", summary.Skipped)
	} else {
		fmt.Println("\n✅ All builds successful")
	}
	os.Exit(0)
}

// collectParseIssues turns discovery's flags into reportable issues.
// printBaseDiagnostic explains an unusable base ref in terms the user can act
// on. The two causes need different advice, which is the only reason the second
// probe exists: recommending fetch-depth for a typo'd branch name sends people
// to change the wrong thing.
func printBaseDiagnostic(base git.BaseStatus) {
	fmt.Printf("\n⚠️  Cannot diff against base ref %q\n", base.Ref)
	switch base.State {
	case git.BaseUnresolvableShallow:
		fmt.Println("   The repository is a shallow clone, so that commit is not present locally.")
		fmt.Println("   Fix: set `fetch-depth: 0` on actions/checkout.")
	case git.BaseUnresolvableNotShallow:
		fmt.Println("   The repository is complete, so the ref itself does not exist:")
		fmt.Println("   a typo, a deleted branch, or a base-ref expression that evaluated unexpectedly.")
		fmt.Println("   Note: fetch-depth will NOT help here.")
	default:
		fmt.Println("   The base-ref probe could not run.")
	}
	if base.Detail != "" {
		fmt.Printf("   git said: %s\n", base.Detail)
	}
}

// parseBuildTimeout validates the build-timeout input.
//
// A malformed, zero or negative value fails fast rather than silently falling
// back to the default: a limit the user thinks they set but did not is worse
// than being told it is wrong.
func parseBuildTimeout(raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("build-timeout %q is not a valid duration (e.g. 90s, 5m): %w", raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("build-timeout must be positive, got %q", raw)
	}
	return d, nil
}

// baseReason is the one-line explanation published in the step summary.
func baseReason(base git.BaseStatus) string {
	switch base.State {
	case git.BaseUnresolvableShallow:
		return fmt.Sprintf("Base ref %q is not present in this shallow clone, so every discovered kustomization was validated. Set `fetch-depth: 0` on actions/checkout to restore change detection.", base.Ref)
	case git.BaseUnresolvableNotShallow:
		return fmt.Sprintf("Base ref %q does not exist in this repository, so every discovered kustomization was validated.", base.Ref)
	default:
		return fmt.Sprintf("The base-ref probe could not run for %q, so every discovered kustomization was validated.", base.Ref)
	}
}

// allDirs returns every discovered kustomization directory, de-duplicated.
// Discovery can emit two entries for one directory (kustomization.yaml and
// kustomization.yml), and the retained-unparseable entries add another route to
// the same Dir.
func allDirs(kustomizations []discovery.KustomizeFile) []string {
	seen := make(map[string]bool, len(kustomizations))
	dirs := make([]string, 0, len(kustomizations))
	for _, kust := range kustomizations {
		if seen[kust.Dir] {
			continue
		}
		seen[kust.Dir] = true
		dirs = append(dirs, kust.Dir)
	}
	return dirs
}

func collectParseIssues(kustomizations []discovery.KustomizeFile) []reporter.ParseIssue {
	var issues []reporter.ParseIssue
	for _, kust := range kustomizations {
		if kust.Unparsed {
			issues = append(issues, reporter.ParseIssue{
				Path: kust.Path, Reason: kust.ParseErr.Error(),
			})
			continue
		}
		for _, fe := range kust.FieldErrs {
			issues = append(issues, reporter.ParseIssue{
				Path:   kust.Path,
				Reason: fmt.Sprintf("%s — references from this field are unknown", fe.Error()),
			})
		}
	}
	return issues
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// setupLogging configures the global logger based on LOG_LEVEL environment variable
func setupLogging() {
	logLevel := getEnv("LOG_LEVEL", "INFO")

	var level slog.Level
	switch logLevel {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO":
		level = slog.LevelInfo
	case "WARN", "WARNING":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	// Create a text handler with the specified level
	opts := &slog.HandlerOptions{
		Level: level,
	}
	handler := slog.NewTextHandler(os.Stderr, opts)
	logger := slog.New(handler)

	// Set as default logger
	slog.SetDefault(logger)

	slog.Debug("Logging configured", "level", logLevel)
}
