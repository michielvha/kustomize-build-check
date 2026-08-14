#!/usr/bin/env bash
# Runs the real binary inside the built image against the fixture repository and
# asserts the action's observable behaviour is unchanged.
#
# This is the only check that can catch a runtime binary going missing: `go test`
# runs on the CI host, not in the image, so it cannot see that `git` or
# `kustomize` is absent from the container.
#
# Usage: smoke.sh <image-ref> [platform] [--capture]
set -euo pipefail

IMAGE="${1:?usage: smoke.sh <image-ref> [platform] [--capture]}"
PLATFORM="${2:-}"
MODE="${3:-assert}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
eval "$("$HERE/make-fixture.sh")"

PLATFORM_ARG=""
if [ -n "$PLATFORM" ] && [ "$PLATFORM" != "--capture" ]; then
  PLATFORM_ARG="--platform $PLATFORM"
fi
[ "$PLATFORM" = "--capture" ] && MODE="--capture"

OUT="$(mktemp -d)"; chmod 0777 "$OUT"

set +e
STDOUT="$(docker run --rm $PLATFORM_ARG \
  -v "$DIR":/github/workspace \
  -v "$OUT":/out \
  -w /github/workspace \
  -u 1001:1001 \
  -e "INPUT_BASE-REF=$BASE_SHA" \
  -e "GITHUB_OUTPUT=/out/gh_output" \
  "$IMAGE" 2>&1)"
EXIT=$?
set -e

echo "$STDOUT"

get() { grep -oE "^$1=[0-9]+" "$OUT/gh_output" 2>/dev/null | cut -d= -f2 || echo ""; }
TOTAL_LINE="$(echo "$STDOUT" | grep -oE 'Summary: [0-9]+ total' | grep -oE '[0-9]+' || echo "")"
SUCCESS="$(get success-count)"; FAILED="$(get failed-count)"; SKIPPED="$(get skipped-count)"

if [ "$MODE" = "--capture" ]; then
  cat > "$HERE/expected.env" <<EOF
# Captured $(date -u +%Y-%m-%dT%H:%M:%SZ) from $IMAGE
EXPECT_TOTAL=$TOTAL_LINE
EXPECT_SUCCESS=$SUCCESS
EXPECT_FAILED=$FAILED
EXPECT_SKIPPED=$SKIPPED
EXPECT_EXIT=$EXIT
EOF
  echo "captured -> $HERE/expected.env"; cat "$HERE/expected.env"; exit 0
fi

# shellcheck disable=SC1091
. "$HERE/expected.env"

fail=0
check() { # name got want
  if [ "$2" != "$3" ]; then echo "FAIL: $1 = '$2', want '$3'"; fail=1; else echo "ok: $1 = $2"; fi
}
check "exit code" "$EXIT" "$EXPECT_EXIT"
check "total"     "$TOTAL_LINE" "$EXPECT_TOTAL"
check "success"   "$SUCCESS" "$EXPECT_SUCCESS"
check "failed"    "$FAILED" "$EXPECT_FAILED"
check "skipped"   "$SKIPPED" "$EXPECT_SKIPPED"

# A missing runtime binary shows up here: the run produces no counts at all.
if [ -z "$SUCCESS" ]; then
  echo "FAIL: no counts were written; the binary likely could not run in the image"
  fail=1
fi

exit "$fail"
