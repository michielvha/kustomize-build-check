# Kustomize Build Check - Design Document

## Overview

A GitHub Action that intelligently validates Kustomize configurations by automatically discovering overlay and base relationships, then running `kustomize build --enable-helm` on affected paths based on git changes.

## Architecture

### Repository Structure (Enterprise-Grade Separation)

This project follows a clean separation of concerns with two repositories:

1. **Tool Repository** (`michielvha/kustomize-build-check`) - **THIS REPO**
   - Contains the Go source code
   - Builds multi-platform binaries (Linux, macOS, Windows for amd64 and arm64)
   - Builds multi-architecture Docker images (linux/amd64, linux/arm64)
   - Publishes binaries to GitHub Releases
   - Publishes Docker images to GitHub Container Registry (GHCR)
   - Uses GitVersion for semantic versioning
   - Uses custom composite actions for build/release pipeline

2. **Action Repository** (`michielvha/kustomize-build-check-action`)
   - Contains only the `action.yml` definition
   - References pre-built Docker images from GHCR
   - Provides the user-facing GitHub Action interface
   - Can version independently from the tool

### Benefits of This Architecture

✅ **Clean Separation**: Tool development is independent from action interface  
✅ **Faster CI/CD**: Action users get pre-built images, no build time  
✅ **Version Flexibility**: Action can pin to specific tool versions  
✅ **Better Security**: Images built, scanned, and signed in source repo  
✅ **Multi-platform**: Binaries available for local use, Docker for GitHub Actions  
✅ **Easier Maintenance**: Update tool without changing user workflows

### Release Pipeline

```
1. Push to main → GitVersion tags repo
2. GoReleaser builds binaries for all platforms
3. docker-release-action builds multi-arch images
4. Artifacts published to GitHub Releases + GHCR
5. Action repo references the published images
```

Uses these custom composite actions:
- `michielvha/gitversion-tag-action` - Semantic versioning from Git history
- `michielvha/goreleaser-action` - Cross-platform binary builds with PGP signing
- `michielvha/docker-release-action` - Multi-architecture Docker image builds

## Problem Statement

When working with Kustomize in GitOps workflows:
1. Changes to a base can break multiple overlays that depend on it
2. Manual testing of all overlays after base changes is error-prone
3. Existing actions require manual path specification
4. No existing solution automatically maps base → overlay relationships
5. Helm chart integration needs explicit testing

## Goals

### Primary Goals
- ✅ **Auto-discovery**: Automatically find all `kustomization.yaml` files
- ✅ **Relationship mapping**: Build a dependency graph (base ← overlay)
- ✅ **Smart testing**: 
  - If base changes → test all dependent overlays
  - If overlay changes → test only that overlay
- ✅ **Helm support**: Run builds with `--enable-helm` flag
- ✅ **Clear feedback**: Show which builds failed and why

### Secondary Goals
- 🎯 Fast execution (parallel builds where possible)
- 🎯 Actionable error messages
- 🎯 JSON output for downstream processing
- 🎯 Support for custom Kustomize versions

## Non-Goals (v1)
- ❌ Deploying resources
- ❌ Validating against Kubernetes API schemas (use kubeconform separately)
- ❌ Security scanning
- ❌ Generating diffs

## Architecture

### High-Level Flow

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Git Change Detection                                     │
│    - Compare HEAD with base-ref (PR base or main)          │
│    - Get list of changed files                             │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. Kustomize Discovery                                      │
│    - Recursively find all kustomization.yaml files          │
│    - Parse each file to extract:                            │
│      • resources (local paths)                              │
│      • bases (deprecated but still used)                    │
│      • components                                           │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Dependency Graph Building                                │
│    - Map each overlay to its bases                          │
│    - Identify which kustomizations are "bases" vs "overlays"│
│    - Build reverse lookup: base → [overlays]                │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Impact Analysis                                          │
│    For each changed file:                                   │
│    - Is it a kustomization.yaml?                            │
│      • Base? → Add all dependent overlays to test set       │
│      • Overlay? → Add only this overlay to test set         │
│    - Is it referenced by a kustomization?                   │
│      • Add the referencing kustomization to test set        │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────┐
│ 5. Build Execution                                          │
│    For each kustomization in test set:                      │
│    - Run: kustomize build --enable-helm <dir>               │
│    - Capture stdout/stderr                                  │
│    - Record success/failure                                 │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────┐
│ 6. Results & Reporting                                      │
│    - Generate summary (success count, failure count)        │
│    - Output detailed errors for failed builds               │
│    - Set GitHub Action outputs (JSON results)               │
│    - Exit with appropriate code                             │
└─────────────────────────────────────────────────────────────┘
```

## Components

### 1. Git Analyzer (`internal/git`)
**Responsibility**: Detect changed files between commits

```go
type GitAnalyzer interface {
    GetChangedFiles(baseRef, headRef string) ([]string, error)
}
```

**Implementation**:
- Use `git diff --name-only` to list changed files
- Handle different scenarios:
  - PR context: compare against `github.event.pull_request.base.sha`
  - Push context: compare against previous commit or main
  - Local testing: compare against HEAD~1 or specified ref

### 2. Kustomize Discovery (`internal/discovery`)
**Responsibility**: Find and parse all kustomization files

```go
type KustomizeFile struct {
    Path      string   // Absolute path to kustomization.yaml
    Dir       string   // Directory containing the file
    Resources []string // Relative paths referenced
    Bases     []string // Deprecated bases field
}

type Discoverer interface {
    FindAll(rootDir string) ([]KustomizeFile, error)
    ParseKustomization(path string) (*KustomizeFile, error)
}
```

**Implementation**:
- Walk filesystem recursively
- Identify files named `kustomization.yaml`, `kustomization.yml`, or `Kustomization`
- Parse YAML to extract:
  - `resources` array
  - `bases` array (if present)
  - `components` array
  - `helmCharts` (to ensure Helm support is needed)

### 3. Dependency Graph (`internal/graph`)
**Responsibility**: Build and query overlay→base relationships

```go
type Node struct {
    Path         string
    IsBase       bool
    Dependencies []string // Paths this node depends on
}

type DependencyGraph struct {
    nodes         map[string]*Node
    reverseLookup map[string][]string // base -> [overlays]
}

type Graph interface {
    Build(files []KustomizeFile) error
    GetDependentOverlays(basePath string) []string
    IsBase(path string) bool
}
```

**Algorithm**:
```
For each kustomization K:
  For each resource R in K.resources:
    If R is a directory containing kustomization.yaml:
      Mark R as a base
      Add K to reverseLookup[R]
```

### 4. Impact Analyzer (`internal/analyzer`)
**Responsibility**: Determine which kustomizations need testing

```go
type ImpactAnalyzer interface {
    GetAffectedKustomizations(
        changedFiles []string,
        graph Graph,
        allKustomizations []KustomizeFile,
    ) []string
}
```

**Logic**:
```
affected = []

For each changed file:
  // Direct kustomization change
  If file is kustomization.yaml:
    dir = directory of file
    
    If graph.IsBase(dir):
      affected += graph.GetDependentOverlays(dir)
    Else:
      affected += [dir]
  
  // Resource file change
  Else:
    For each kustomization K:
      If changed file is in K.resources:
        affected += [K.dir]

Return unique(affected)
```

### 5. Builder (`internal/builder`)
**Responsibility**: Execute kustomize builds and collect results

```go
type BuildResult struct {
    Path     string
    Success  bool
    Output   string
    Error    string
    Duration time.Duration
}

type Builder interface {
    Build(path string, enableHelm bool) BuildResult
    BuildAll(paths []string, enableHelm bool, parallel bool) []BuildResult
}
```

**Implementation**:
- Execute `kustomize build --enable-helm <path>`
- Capture both stdout and stderr
- Set timeout per build (e.g., 2 minutes)
- Optional: parallel execution with worker pool

### 6. Reporter (`internal/reporter`)
**Responsibility**: Format and output results

```go
type Reporter interface {
    GenerateSummary(results []BuildResult) Summary
    PrintResults(results []BuildResult)
    SetGitHubOutputs(results []BuildResult) error
}

type Summary struct {
    Total   int
    Success int
    Failed  int
    Results []BuildResult
}
```

**Output formats**:
- Console: Human-readable with colors
- GitHub Actions: Set outputs for downstream jobs
- JSON: For programmatic consumption

## Data Flow

```
main.go
  ├─> Read GitHub Action inputs (base-ref, enable-helm, etc.)
  ├─> GitAnalyzer.GetChangedFiles()
  ├─> Discoverer.FindAll()
  ├─> Graph.Build()
  ├─> ImpactAnalyzer.GetAffectedKustomizations()
  ├─> Builder.BuildAll()
  ├─> Reporter.PrintResults()
  └─> Reporter.SetGitHubOutputs()
```

## Example Scenarios

### Scenario 1: Base Changes
```
Changed files:
  - base/common/kustomization.yaml

Discovered kustomizations:
  - base/common/ (base)
  - overlays/dev/ (uses base/common)
  - overlays/prod/ (uses base/common)
  - overlays/staging/ (uses base/common)

Impact:
  base/common is a base
  → Test: overlays/dev, overlays/prod, overlays/staging

Builds:
  ✅ overlays/dev
  ✅ overlays/prod
  ❌ overlays/staging (missing required field)
```

### Scenario 2: Overlay Changes
```
Changed files:
  - overlays/dev/kustomization.yaml
  - overlays/dev/configmap.yaml

Impact:
  overlays/dev is an overlay
  → Test: overlays/dev only

Builds:
  ✅ overlays/dev
```

### Scenario 3: Resource File Changes
```
Changed files:
  - base/common/deployment.yaml

Impact:
  deployment.yaml is referenced by base/common/kustomization.yaml
  base/common is a base
  → Test: overlays/dev, overlays/prod, overlays/staging

Builds:
  ✅ overlays/dev
  ✅ overlays/prod
  ✅ overlays/staging
```

## Edge Cases

1. **Circular dependencies**: Detect and error out (Kustomize doesn't support this)
2. **Missing bases**: Kustomize will fail the build (expected behavior)
3. **No changes in Kustomize files**: Skip builds, report success
4. **Deleted files**: Treat as changes to the kustomization that referenced them
5. **Renamed files**: Git shows as delete + add; handle both

## Testing Strategy

### Unit Tests
- `git`: Mock git commands, test file list parsing
- `discovery`: Test YAML parsing, file walking
- `graph`: Test dependency resolution with various structures
- `analyzer`: Test impact calculation with mock graphs
- `builder`: Mock kustomize command execution

### Integration Tests
Create test fixtures:
```
testdata/
  simple/
    base/
      kustomization.yaml
    overlay/
      kustomization.yaml
  
  multi-base/
    base-a/
    base-b/
    overlay/
  
  helm/
    base/
      kustomization.yaml (with helmCharts)
    overlay/
```

### E2E Tests
- GitHub Actions workflow in `.github/workflows/test.yml`
- Test against real Kustomize files
- Verify action outputs

## Implementation Plan

### Phase 1: Core Discovery
- [x] Implement `internal/discovery`
- [x] Implement `internal/graph`
- [x] Unit tests for both
- [ ] CLI tool to print discovered structure

### Phase 2: Change Detection
- [x] Implement `internal/git`
- [x] Implement `internal/analyzer`
- [ ] Unit tests
- [ ] CLI tool to print affected paths

### Phase 3: Build Execution
- [x] Implement `internal/builder`
- [x] Implement `internal/reporter`
- [ ] Integration tests
- [x] Full CLI functionality

### Phase 4: GitHub Action Integration
- [x] Wire up action inputs/outputs
- [x] Dockerfile optimization
- [x] Documentation
- [x] Example workflows

### Phase 5: Polish & Release
- [ ] Error message improvements
- [ ] Performance optimization
- [ ] GitHub Marketplace publishing
- [ ] Blog post / announcement

## Configuration

### Action Inputs
```yaml
inputs:
  base-ref:
    description: 'Git ref to compare against'
    default: auto-detect
  
  enable-helm:
    description: 'Pass --enable-helm to kustomize'
    default: 'true'
  
  kustomize-version:
    description: 'Kustomize version to install'
    default: 'latest'
  
  fail-on-error:
    description: 'Fail workflow if any build fails'
    default: 'true'
  
  root-dir:
    description: 'Root directory to search'
    default: '.'
```

### Action Outputs
```yaml
outputs:
  results:
    description: 'JSON array of build results'
  
  failed-count:
    description: 'Number of failed builds'
  
  success-count:
    description: 'Number of successful builds'
```

## Technology Choices

### Go vs JavaScript vs Python

**Why Go?**
✅ Native performance for file I/O and git operations
✅ Strong standard library for CLI tools
✅ Static binaries (easy distribution across platforms)
✅ Excellent testing support
✅ You're proficient in Go
✅ Kustomize itself is written in Go (could import as library later)

**Why NOT JavaScript?**
- Requires Node.js runtime
- Most GitHub Actions use JS for simplicity, but our logic is complex enough to benefit from Go's type safety

**Why NOT Python?**
- Runtime dependencies
- Slower for file operations
- Less idiomatic for CLI tools at scale

**Decision: Go** ✅

### Distribution Strategy: Dual-Repository Pattern

**Chosen Approach**: Tool repository + Action repository

#### Tool Repository (`michielvha/kustomize-build-check`)
- Go source code
- GoReleaser for multi-platform binaries
- Docker multi-arch images
- Published to GitHub Releases + GHCR

**Pros:**
✅ Users can download binary for local use
✅ Multi-platform support (Linux, macOS, Windows)
✅ Pre-built Docker images for fast GitHub Actions execution
✅ Images built once, used many times
✅ Proper versioning and release management

#### Action Repository (`michielvha/kustomize-build-check-action`)
- Only `action.yml` and README
- References Docker images from GHCR
- Clean user interface

**Pros:**
✅ Faster for users (no build time)
✅ Separation of concerns (tool vs action interface)
✅ Can version independently
✅ Users can pin to specific versions
✅ No git ownership issues (pre-built images)

### Release Pipeline Components

1. **GitVersion** (`michielvha/gitversion-tag-action`)
   - Semantic versioning from Git history
   - Automatic tagging on main branch
   - Supports feature branches with pre-release tags

2. **GoReleaser** (`michielvha/goreleaser-action`)
   - Cross-compilation for multiple OS/arch combinations
   - PGP signing of checksums
   - GitHub Releases creation
   - Changelog generation from conventional commits

3. **Docker Multi-Arch** (`michielvha/docker-release-action`)
   - Builds for linux/amd64 and linux/arm64
   - Pushes to GitHub Container Registry
   - Uses buildx for efficient builds
   - Proper OCI labels for metadata

### Docker Image Architecture

The Docker images include:
- **Base**: Alpine Linux 3.22 (minimal footprint)
- **Git**: For change detection
- **Kustomize**: v5.3.0 (configurable)
- **Helm**: v3.16.2 (for --enable-helm support)
- **Binary**: Pre-built kustomize-build-check from GoReleaser

The binary itself handles git safe.directory configuration at runtime, making it work in both:
- Docker containers (with mounted volumes)
- Standalone execution (local development/testing)

**Decision: Dual-repository with pre-built images** ✅

## Success Metrics

- ✅ Correctly identifies overlay dependencies
- ✅ Reduces CI time vs testing all overlays always
- ✅ Clear error messages when builds fail
- ✅ Works with Helm charts
- ✅ Action completes in < 30s for typical repos
- ✅ Zero false positives (doesn't test unaffected overlays)
- ✅ Zero false negatives (doesn't miss affected overlays)

## Future Enhancements (v2+)

- 🔮 Parallel build execution (worker pool)
- 🔮 Caching of Kustomize discovery results
- 🔮 Support for remote bases (Git URLs)
- 🔮 Integration with kubeconform for schema validation
- 🔮 PR comments with build results
- 🔮 Diff generation between base and head
- 🔮 Support for `kustomize build --load-restrictor=LoadRestrictionsNone`
- 🔮 Custom build flags via action inputs

## Open Questions

1. **Should we support kustomize as a library instead of shelling out?**
   - Pro: Faster, more control
   - Con: Version lock-in, complexity
   - **Decision**: Shell out for v1, library for v2 if needed

2. **How to handle very large repos (1000+ kustomizations)?**
   - **Decision**: Implement discovery caching in v2

3. **Should we validate the generated YAML?**
   - **Decision**: No, out of scope. Users should use kubeconform separately

## References

- [Kustomize Documentation](https://kubectl.docs.kubernetes.io/references/kustomize/)
- [GitHub Actions - Creating a Docker container action](https://docs.github.com/en/actions/creating-actions/creating-a-docker-container-action)
- [anarcher/kustomize-check-action](https://github.com/anarcher/kustomize-check-action) - Prior art
- [mattwithoos/kusteval](https://github.com/mattwithoos/kusteval) - Validation approach
