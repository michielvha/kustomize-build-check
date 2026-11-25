# kustomize-build-check

[![Build and Release](https://github.com/michielvha/kustomize-build-check/actions/workflows/build-release.yml/badge.svg)](https://github.com/michielvha/kustomize-build-check/actions/workflows/build-release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/michielvha/kustomize-build-check)](https://goreportcard.com/report/github.com/michielvha/kustomize-build-check)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

kustomize-build-check finds the Kustomize overlays a change affects and runs `kustomize build` on each
of them. This repository holds the Go source and publishes the container image. To run the check in
GitHub Actions, use [kustomize-build-check-action](https://github.com/michielvha/kustomize-build-check-action).

## How it works

Each stage is one package under `internal/`:

1. `git` lists the files changed between the base ref and `HEAD`.
2. `discovery` finds and parses every kustomization file under the root directory.
3. `graph` records which kustomization references which.
4. `analyzer` marks the kustomizations a changed file belongs to or is referenced by, plus everything
   that depends on them.
5. `builder` runs `kustomize build` on the marked paths.
6. `reporter` prints the results and writes the GitHub Actions outputs and step summary.

[design.md](design.md) describes the design. The behavior specifications are in [docs/specs](docs/specs/)
and the architecture decisions in [docs/decisions](docs/decisions/).

## Development

You need the Go version in [go.mod](go.mod). The integration tests build real repositories, so they
also need `git` and `kustomize` on `PATH`, and skip themselves when `kustomize` is missing.

```bash
go build -o kustomize-build-check ./cmd/action
go test ./...
docker build -t kustomize-build-check:dev .
```

The binary reads its inputs from the `INPUT_*` variables GitHub Actions sets, such as `INPUT_BASE-REF`
and `INPUT_ROOT-DIR`. [cmd/action/main.go](cmd/action/main.go) reads them all with their defaults. The
names contain hyphens, which `export` rejects, so pass them with `env`:

```bash
env 'INPUT_BASE-REF=HEAD~1' 'INPUT_ROOT-DIR=.' ./kustomize-build-check
```

Outside GitHub Actions the results go to the terminal only. Set `LOG_LEVEL` to `DEBUG`, `INFO` (the
default), `WARN` or `ERROR`. At `DEBUG` the tool logs how it built the dependency graph and which
reference made each kustomization count as affected.

## Releases

A push to `main` cuts a release unless it only touches documentation (see `paths-ignore` in
[build-release.yml](.github/workflows/build-release.yml)). GitVersion tags the commit, with the version bump taken from the
conventional commit messages (see [gitversion.yml](gitversion.yml)). GoReleaser then publishes
binaries for Linux, macOS and Windows on amd64 and arm64 to GitHub Releases, and the workflow pushes a
linux/amd64 and linux/arm64 image to `ghcr.io/michielvha/kustomize-build-check`.

The action repository pins the image by commit SHA. Action users get a release only after that pin is
bumped in its `action.yml`.

## Contributing

Behavior changes start with a specification in [docs/specs](docs/specs/). Use conventional commit
messages, since they decide the next version. Report bugs and request features in this repository's
[issues](https://github.com/michielvha/kustomize-build-check/issues).
