#!/usr/bin/env bash
# Asserts the image's runtime contract: what it runs as, what it ships, and that
# nothing in the entrypoint hardcodes the workload name.
#
# Assertions marked EXPECT_FAIL are known-red against the current alpine image
# and become required when the base swap lands. Marking them keeps the baseline
# honest rather than pretending it already passes.
set -euo pipefail

IMAGE="${1:?usage: contract.sh <image-ref>}"
WORKLOAD="$(grep -E '^\s+workload_name:' "$(dirname "${BASH_SOURCE[0]}")/../../vega.yaml" | sed 's/.*"\(.*\)"/\1/')"

fail=0
check() { # name got want [EXPECT_FAIL]
  if [ "$2" = "$3" ]; then
    echo "ok: $1"
  elif [ "${4:-}" = "EXPECT_FAIL" ]; then
    echo "known-red (expected until the base swap): $1 = '$2', want '$3'"
  else
    echo "FAIL: $1 = '$2', want '$3'"; fail=1
  fi
}

# Config.User may hold a name rather than a number, and a name can map to any
# uid. What the workspace permissions actually depend on is the effective uid.
USER_ID="$(docker run --rm --entrypoint /bin/sh "$IMAGE" -c 'id -u' 2>/dev/null || echo "?")"
check "effective UID is 1001" "$USER_ID" "1001"

EP="$(docker inspect --format '{{json .Config.Entrypoint}}' "$IMAGE")"
LEN="$(echo "$EP" | tr ',' '\n' | wc -l | tr -d ' ')"
check "entrypoint is a single element (no shell)" "$LEN" "1" EXPECT_FAIL
if echo "$EP" | grep -q "$WORKLOAD"; then
  echo "FAIL: entrypoint hardcodes the workload name ($WORKLOAD): $EP"; fail=1
else
  echo "ok: entrypoint does not hardcode the workload name"
fi

# Every binary the tool shells out to must be present AND executable as the
# runtime user. `ls` is not enough: a wrong interpreter shows up only on exec.
for bin in git kustomize helm; do
  if docker run --rm --entrypoint "$bin" -u 1001:1001 "$IMAGE" --version >/dev/null 2>&1 \
     || docker run --rm --entrypoint "$bin" -u 1001:1001 "$IMAGE" version >/dev/null 2>&1; then
    echo "ok: $bin runs as UID 1001"
  else
    echo "FAIL: $bin is missing or not executable as UID 1001"; fail=1
  fi
done

# No Go toolchain should survive into the runtime image.
if docker run --rm --entrypoint /bin/sh "$IMAGE" -c 'command -v go' >/dev/null 2>&1; then
  echo "FAIL: a Go toolchain is present in the runtime image"; fail=1
else
  echo "ok: no Go toolchain in the runtime image"
fi

exit "$fail"
