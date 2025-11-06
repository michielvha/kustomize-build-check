package main

import (
	"fmt"
	"os"

	"github.com/michielvha/kustomize-build-check/internal/analyzer"
	"github.com/michielvha/kustomize-build-check/internal/builder"
	"github.com/michielvha/kustomize-build-check/internal/discovery"
	"github.com/michielvha/kustomize-build-check/internal/git"
	"github.com/michielvha/kustomize-build-check/internal/graph"
	"github.com/michielvha/kustomize-build-check/internal/reporter"
)

func main() {
	fmt.Println("🔍 Kustomize Build Check")
	fmt.Println()

	// Read inputs from environment (GitHub Actions sets INPUT_* vars)
	baseRef := getEnv("INPUT_BASE-REF", "")
	enableHelm := getEnv("INPUT_ENABLE-HELM", "true") == "true"
	failOnError := getEnv("INPUT_FAIL-ON-ERROR", "true") == "true"
	rootDir := getEnv("INPUT_ROOT-DIR", ".")

	// 1. Detect changed files
	fmt.Println("📝 Detecting changed files...")
	gitAnalyzer := git.New()
	changedFiles, err := gitAnalyzer.GetChangedFiles(baseRef, "HEAD")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error detecting changes: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   Found %d changed files\n", len(changedFiles))

	// 2. Discover all kustomizations
	fmt.Println("\n🔎 Discovering kustomization files...")
	disc := discovery.New()
	kustomizations, err := disc.FindAll(rootDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering kustomizations: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   Found %d kustomization files\n", len(kustomizations))

	// 3. Build dependency graph
	fmt.Println("\n🕸️  Building dependency graph...")
	g := graph.New()
	if err := g.Build(kustomizations); err != nil {
		fmt.Fprintf(os.Stderr, "Error building graph: %v\n", err)
		os.Exit(1)
	}

	// 4. Analyze impact
	fmt.Println("\n📊 Analyzing impact...")
	impactAnalyzer := analyzer.New()
	affectedPaths := impactAnalyzer.GetAffectedKustomizations(changedFiles, g, kustomizations)

	if len(affectedPaths) == 0 {
		fmt.Println("   No kustomizations affected by changes")
		fmt.Println("\n✅ All checks passed")
		os.Exit(0)
	}

	fmt.Printf("   %d kustomization(s) need testing:\n", len(affectedPaths))
	for _, path := range affectedPaths {
		fmt.Printf("     - %s\n", path)
	}

	// 5. Build affected kustomizations
	fmt.Println("\n🔨 Running kustomize build...")
	bldr := builder.New()
	results := bldr.BuildAll(affectedPaths, enableHelm)

	// 6. Report results
	rep := reporter.New()
	rep.PrintResults(results)

	// Set GitHub Actions outputs
	if err := rep.SetGitHubOutputs(results); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to set GitHub outputs: %v\n", err)
	}

	// Determine exit code
	summary := rep.GenerateSummary(results)
	if failOnError && summary.Failed > 0 {
		fmt.Println("\n❌ Some builds failed")
		os.Exit(1)
	}

	fmt.Println("\n✅ All builds successful")
	os.Exit(0)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
