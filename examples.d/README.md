# Examples

This directory contains example GitHub Actions workflows demonstrating how to use the kustomize-build-check action.

## Basic Example

[`.github/workflows/basic.yml`](basic.yml) - Simple validation on pull requests

## Advanced Examples

[`.github/workflows/with-kubeconform.yml`](with-kubeconform.yml) - Combine with kubeconform for schema validation

[`.github/workflows/continue-on-error.yml`](continue-on-error.yml) - Continue workflow even if builds fail

[`.github/workflows/custom-base-ref.yml`](custom-base-ref.yml) - Use a custom base reference

## Test Repository Structure

The examples assume a repository structure like:

```
kubernetes/
  base/
    common/
      kustomization.yaml
      deployment.yaml
      service.yaml
  overlays/
    dev/
      kustomization.yaml
      patches/
    staging/
      kustomization.yaml
    prod/
      kustomization.yaml
```
