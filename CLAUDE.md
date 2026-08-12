# kustomize-build-check, constitution

This repository is a **go-cli-tool** (Go) published as a container image and consumed by
the thin wrapper action in `michielvha/kustomize-build-check-action`.

It is a Vega-managed repo, but **only for the SDD loop**: `/spec` → `/plan` → `/implement`
→ `/review`. This is a personal OSS project, not an Engie platform workload. It has no
GitOps overlays, no secret manager, no cluster or registry bindings, and no team ownership
block. Agents must not ask about them or try to derive them.

Keep this file narrative and small; structured bindings live in `vega.yaml`.

## Non-negotiables

- **Specs are the source of truth.** Behavior changes start at `docs/specs/`, not at the code.
- **The reviewer subagent's write scope** is `summaries/**` and `plans/*.md`. Findings are
  recorded as review notes, never as silent source edits.
- **Bindings have one home: `vega.yaml`.** Nothing hardcodes the image name or the consumer
  repo; read it through the `read-constitution` skill.
- **Reuse before you build.** Walk the ladder: is it needed at all? → does the stdlib do it?
  → does an already-installed dependency? → can it be one line? → only then write the
  minimum. This repo has exactly one direct dependency (`gopkg.in/yaml.v3`); keep it that way
  unless a new one is justified in a spec. Never a licence to drop input validation or
  error handling.

## This repo's specifics

- **Archetype:** go-cli-tool
- **Language:** Go (see `go.mod` for the pinned version)
- **Workload slug:** `kustomize-build-check`
- **Architecture:** see [design.md](design.md). The pipeline is
  `git diff → discovery → dependency graph → impact analysis → build → report`,
  one package per stage under `internal/`.

## Correctness bar

This tool is a CI gate, so the failure modes are asymmetric and the specs must say which
side an ambiguity falls on:

- **A false pass is worse than a false fail.** Reporting green on a repo where
  `kustomize build` would fail defeats the tool's only purpose. Never narrow the set of
  validated paths without proving the narrowing cannot hide a real breakage.
- **A false fail trains people to ignore the check**, so it is not cheap either. Paths a
  change legitimately removed are reported as *skipped*, never as failures.
- Deletions stay in the git diff on purpose. See the comment in `internal/git/git.go`.

## Release flow

Push to `main` → GitVersion tags (conventional commits drive the bump, see `gitversion.yml`)
→ GoReleaser builds → image pushed to GHCR tagged with the commit SHA and the semver →
the action repo's `action.yml` pin is bumped to that SHA.

## What NOT to do

- Don't duplicate `vega.yaml` values into this file.
- Don't add GitOps, secrets, cluster or team bindings; they do not apply to this repo.
- Don't let the integration tests silently skip in CI. They need `git` and `kustomize` on
  PATH, and the release workflow installs kustomize so they actually run.
