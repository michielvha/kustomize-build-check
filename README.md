# Kustomize Build Check

[![Build and Release](https://github.com/michielvha/kustomize-build-check/actions/workflows/build-release.yml/badge.svg)](https://github.com/michielvha/kustomize-build-check/actions/workflows/build-release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/michielvha/kustomize-build-check)](https://goreportcard.com/report/github.com/michielvha/kustomize-build-check)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

CLI tool for automatically discovering and validating Kustomize overlays with intelligent change detection.

**Looking to use this in GitHub Actions?** → See the **[kustomize-build-check-action](https://github.com/michielvha/kustomize-build-check-action)** repository.

This repository contains the source code and build pipeline. The action repository provides a clean interface for GitHub Actions users.

## Overview

Intelligently validates Kustomize configurations by:
- 🔍 Auto-discovering all Kustomize files and their dependencies
- 🧠 Smart testing based on what changed (bases → all overlays, overlays → just that one)
- ⚡ Helm chart support with `--enable-helm`
- 📊 Clear build results and error reporting
- ⏭️ Skipping paths the change removed, so deleting or renaming a directory does not fail the check

## Architecture

See [design.md](design.md) for detailed architecture documentation.

**Repository Structure:**
- **Tool Repository** (this one): Go source, binaries, Docker images
- **Action Repository**: GitHub Action interface referencing GHCR images generated via source code repository

**Release Pipeline:**
1. Push to `main` → GitVersion tags the repo
2. GoReleaser builds multi-platform binaries
3. Docker images built for linux/amd64 and linux/arm64
4. Published to GitHub Releases + GHCR

## Development

### Prerequisites
- Go 1.23+
- Docker (for testing containers)
- Kustomize CLI

### Building

```bash
# Build binary
go build -o kustomize-build-check ./cmd/action

# Run tests
# The integration tests build real repositories, so they need git and the
# kustomize CLI on PATH. They skip themselves if kustomize is missing.
go test ./...

# Build Docker image locally
docker build -f Dockerfile -t kustomize-build-check:dev .
```

### Running Locally

```bash
# Set environment variables (simulates GitHub Actions)
export INPUT_BASE-REF="HEAD~1"
export INPUT_ENABLE-HELM="true"
export INPUT_ROOT-DIR="."

# Run the binary
./kustomize-build-check

# Enable debug logging
LOG_LEVEL=DEBUG ./kustomize-build-check
```

### Logging

The tool supports structured logging with configurable log levels via the `LOG_LEVEL` environment variable:

- **`INFO`** (default): Shows standard progress output
- **`DEBUG`**: Detailed execution information including:
  - Dependency graph construction
  - Impact analysis decisions
  - File processing steps
  - Kustomize build commands and results
- **`WARN`**: Warnings only
- **`ERROR`**: Errors only

**Example with debug logging:**
```bash
LOG_LEVEL=DEBUG ./kustomize-build-check
```

This will show detailed logs like:
```
time=2025-11-06T22:23:21.497+01:00 level=DEBUG msg="Building dependency graph" kustomization_count=23
time=2025-11-06T22:23:21.497+01:00 level=DEBUG msg="Found dependencies" kustomization=/path/to/overlay dependencies=[../../base]
time=2025-11-06T22:23:21.497+01:00 level=DEBUG msg="Added reverse lookup" base=/path/to/base dependent=/path/to/overlay
```

### Project Structure

```
.
├── cmd/action/          # Main entry point
├── internal/
│   ├── analyzer/        # Impact analysis
│   ├── builder/         # Kustomize build execution
│   ├── discovery/       # Find kustomization files
│   ├── git/             # Git operations
│   ├── graph/           # Dependency graph
│   └── reporter/        # Results output
├── .goreleaser.yml      # Multi-platform binary builds
├── Dockerfile           # Production multi-arch image
└── design.md            # Architecture documentation
```

## Release Process

Releases are automated via GitHub Actions using custom composite actions:

1. **Push to `main`** → Triggers [build-release.yml](.github/workflows/build-release.yml)
2. **GitVersion** → Creates semantic version tag
3. **GoReleaser** → Builds binaries for all platforms
4. **Docker** → Builds and pushes multi-arch images to GHCR
5. **Action Repo** → Update to reference new version (manual)

## Contributing

Contributions welcome!

1. Check [design.md](design.md) for architecture details
2. Fork the repository
3. Create a feature branch (`feat/my-feature`)
4. Make your changes with tests
5. Use conventional commits (`feat:`, `fix:`, `chore:`)
6. Submit a pull request

## Related Projects

- [kustomize-build-check-action](https://github.com/michielvha/kustomize-build-check-action) - GitHub Action interface
- [kustomize](https://github.com/kubernetes-sigs/kustomize) - Kubernetes native configuration management
