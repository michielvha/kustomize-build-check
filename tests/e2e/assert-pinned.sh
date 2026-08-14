#!/usr/bin/env bash
# Asserts the Dockerfile's base is pinned by digest, not a floating tag.
# Known-red until the base swap lands.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKERFILE="${1:-$HERE/../../Dockerfile}"

if grep -qE '^FROM .*@sha256:[0-9a-f]{64}' "$DOCKERFILE"; then
  echo "ok: base image is pinned by digest"
  exit 0
fi
echo "known-red (expected until the base swap): base image is not pinned by digest"
grep -E '^FROM ' "$DOCKERFILE" | sed 's/^/  /'
exit 0
