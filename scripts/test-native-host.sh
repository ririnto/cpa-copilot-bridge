#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH='' cd -- "$SCRIPT_DIR/.." && pwd)
TARGET_GOOS=${NATIVE_HOST_GOOS:-}
TARGET_GOARCH=${NATIVE_HOST_GOARCH:-}
NATIVE_HOST_IMAGE=${NATIVE_HOST_IMAGE:-golang:1.26-bookworm}
TEST_SELECTOR='^(TestNativeHostProtocolRoundTrips|TestNativeHostOAuth.*|TestFilteredChildEnvironment|TestNativeHostCanonicalResponsesRouting|TestNativeHostConfiguredCanonicalResponsesCompaction|TestNativeHostPluginResponsesWebsocket|TestNativeHostToolAndSystemCompatibility|TestNativeHostPreservesIncompleteResponsesTerminal)$'
TEST_LIST_PATTERN='^(TestNativeHost|TestFilteredChildEnvironment)'
REQUIRED_SEEDS='manifest.json auth.json legacy-auth.json model-catalog.json responses-request.json responses-history.json responses.json responses.sse chat.json chat.sse messages.json messages.sse responses-compaction.json responses-compaction.sse'
REQUIRED_TESTS='TestNativeHostProtocolRoundTrips TestNativeHostOAuthContinuityPersistsAcrossRestart TestNativeHostOAuthExcludedModelsFilterPluginModels TestNativeHostOAuthSettingsOverrideCopilotModelContext TestFilteredChildEnvironment TestNativeHostCanonicalResponsesRouting TestNativeHostConfiguredCanonicalResponsesCompaction TestNativeHostPluginResponsesWebsocket TestNativeHostToolAndSystemCompatibility TestNativeHostPreservesIncompleteResponsesTerminal'

if [ -z "$TARGET_GOOS" ] || [ -z "$TARGET_GOARCH" ]; then
  case "$(uname -s)" in
    Linux) DETECTED_GOOS=linux ;;
    Darwin) DETECTED_GOOS=darwin ;;
    *)
      printf 'error: native host tests require Linux or macOS\n' >&2
      exit 1
      ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) DETECTED_GOARCH=amd64 ;;
    aarch64 | arm64) DETECTED_GOARCH=arm64 ;;
    *)
      printf 'error: unsupported native host architecture: %s\n' "$(uname -m)" >&2
      exit 1
      ;;
  esac
  TARGET_GOOS=${TARGET_GOOS:-$DETECTED_GOOS}
  TARGET_GOARCH=${TARGET_GOARCH:-$DETECTED_GOARCH}
fi

case "$TARGET_GOOS/$TARGET_GOARCH" in
  linux/amd64 | linux/arm64 | darwin/amd64 | darwin/arm64) ;;
  *)
    printf 'error: unsupported native host target: %s/%s\n' "$TARGET_GOOS" "$TARGET_GOARCH" >&2
    exit 1
    ;;
esac

case "$TARGET_GOOS" in
  linux) PLUGIN_EXT=so ;;
  darwin) PLUGIN_EXT=dylib ;;
esac

PLUGIN="$REPO_DIR/build/plugins/$TARGET_GOOS/$TARGET_GOARCH/cliproxyapi-copilot.$PLUGIN_EXT"
PREPARED_DIR="$REPO_DIR/.cache/native-host/$TARGET_GOOS/$TARGET_GOARCH"
CPA_BINARY="$PREPARED_DIR/cli-proxy-api"
INTEGRATION_BINARY="$PREPARED_DIR/integration.test"

for artifact in "$PLUGIN" "$CPA_BINARY" "$INTEGRATION_BINARY"; do
  if [ ! -f "$artifact" ]; then
    printf 'error: required native host artifact is missing: %s\n' "$artifact" >&2
    printf 'Build the plugin and run scripts/prepare-native-host.sh first.\n' >&2
    exit 1
  fi
done

if ! git -C "$REPO_DIR" ls-files --error-unmatch -- config/config.yaml >/dev/null 2>&1 || [ ! -f "$REPO_DIR/config/config.yaml" ]; then
  printf 'error: tracked native host config/config.yaml is missing\n' >&2
  exit 1
fi

if [ ! -x "$CPA_BINARY" ] || [ ! -x "$INTEGRATION_BINARY" ]; then
  printf 'error: prepared native host binaries are not executable\n' >&2
  exit 1
fi

TEMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/cliproxyapi-native-host.XXXXXX")
cleanup() {
  if [ -d "$TEMP_ROOT/source" ]; then
    chmod -R u+w "$TEMP_ROOT/source"
  fi
  rm -rf "$TEMP_ROOT"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM

SNAPSHOT="$TEMP_ROOT/source"
SEED_LIST="$TEMP_ROOT/seed-paths"
mkdir -p \
  "$SNAPSHOT/config" \
  "$SNAPSHOT/integration/testdata/native/v1" \
  "$SNAPSHOT/build/plugins/$TARGET_GOOS/$TARGET_GOARCH" \
  "$SNAPSHOT/.cache/native-host/$TARGET_GOOS/$TARGET_GOARCH" \
  "$TEMP_ROOT/home" \
  "$TEMP_ROOT/tmp"

cp "$REPO_DIR/config/config.yaml" "$SNAPSHOT/config/config.yaml"
cp "$PLUGIN" "$SNAPSHOT/build/plugins/$TARGET_GOOS/$TARGET_GOARCH/cliproxyapi-copilot.$PLUGIN_EXT"
cp "$CPA_BINARY" "$SNAPSHOT/.cache/native-host/$TARGET_GOOS/$TARGET_GOARCH/cli-proxy-api"
cp "$INTEGRATION_BINARY" "$SNAPSHOT/.cache/native-host/$TARGET_GOOS/$TARGET_GOARCH/integration.test"

git -C "$REPO_DIR" ls-files --cached --others --exclude-standard -- \
  'integration/testdata/native/v1/*.json' \
  'integration/testdata/native/v1/*.sse' > "$SEED_LIST"
if [ ! -s "$SEED_LIST" ]; then
  printf 'error: native fixture seeds are missing from the worktree\n' >&2
  exit 1
fi

for seed in $REQUIRED_SEEDS; do
  if ! grep -Fqx -- "integration/testdata/native/v1/$seed" "$SEED_LIST"; then
    printf 'error: required native fixture seed is missing: %s\n' "$seed" >&2
    exit 1
  fi
done

while IFS= read -r relative_path; do
  case "$relative_path" in
    integration/testdata/native/v1/*.json | integration/testdata/native/v1/*.sse) ;;
    *)
      printf 'error: unexpected path in native fixture seed list: %s\n' "$relative_path" >&2
      exit 1
      ;;
  esac
  seed_name=${relative_path##*/}
  case "$seed_name" in
    *[!A-Za-z0-9._-]*)
      printf 'error: unsupported character in native fixture seed path: %s\n' "$relative_path" >&2
      exit 1
      ;;
  esac
  if [ ! -f "$REPO_DIR/$relative_path" ]; then
    printf 'error: native fixture seed is missing from the worktree: %s\n' "$relative_path" >&2
    exit 1
  fi
  cp "$REPO_DIR/$relative_path" "$SNAPSHOT/$relative_path"
done < "$SEED_LIST"

chmod -R a-w "$SNAPSHOT"

if [ "$TARGET_GOOS" = linux ]; then
  command -v docker >/dev/null 2>&1 || {
    printf 'error: Docker is required for offline Linux native host tests\n' >&2
    exit 1
  }
  printf 'Running prepared native host tests in a network-isolated Linux container\n'
  docker run --rm --pull=never --platform="$TARGET_GOOS/$TARGET_GOARCH" --network=none --read-only \
    --tmpfs /tmp:rw,exec,nosuid,size=1g,mode=1777 \
    --user "$(id -u):$(id -g)" \
    -v "$SNAPSHOT:/src:ro" \
    -w /src/integration \
    --entrypoint /usr/bin/env \
    "$NATIVE_HOST_IMAGE" \
    -i \
    PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    HOME=/tmp \
    TMPDIR=/tmp \
    CPA_BINARY="/src/.cache/native-host/$TARGET_GOOS/$TARGET_GOARCH/cli-proxy-api" \
    "/src/.cache/native-host/$TARGET_GOOS/$TARGET_GOARCH/integration.test" \
    -test.list "$TEST_LIST_PATTERN" > "$TEMP_ROOT/test-list"
else
  printf 'Running prepared native host tests with a sanitized macOS environment\n'
  SANDBOX_EXEC=/usr/bin/sandbox-exec
  SANDBOX_PROFILE='(version 1)
(allow default)
(deny network*)
(allow network-bind (local ip "localhost:*"))
(allow network-outbound (remote ip "localhost:*"))
(allow network-inbound (local ip "localhost:*"))'
  if [ ! -x "$SANDBOX_EXEC" ]; then
    printf 'error: sandbox-exec is required for offline macOS native host tests\n' >&2
    exit 1
  fi
  if ! env -i \
    PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin \
    HOME="$TEMP_ROOT/home" \
    TMPDIR="$TEMP_ROOT/tmp" \
    "$SANDBOX_EXEC" -p "$SANDBOX_PROFILE" /usr/bin/true; then
    printf 'error: macOS network sandbox profile is unavailable\n' >&2
    exit 1
  fi
  (
    cd "$SNAPSHOT/integration"
    env -i \
      PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin \
      HOME="$TEMP_ROOT/home" \
      TMPDIR="$TEMP_ROOT/tmp" \
      CPA_BINARY="$SNAPSHOT/.cache/native-host/$TARGET_GOOS/$TARGET_GOARCH/cli-proxy-api" \
      "$SANDBOX_EXEC" -p "$SANDBOX_PROFILE" \
      "$SNAPSHOT/.cache/native-host/$TARGET_GOOS/$TARGET_GOARCH/integration.test" \
      -test.list "$TEST_LIST_PATTERN" > "$TEMP_ROOT/test-list"
  )
fi

for test_name in $REQUIRED_TESTS; do
  if ! grep -Fqx -- "$test_name" "$TEMP_ROOT/test-list"; then
    printf 'error: prepared integration binary is missing required test: %s\n' "$test_name" >&2
    exit 1
  fi
done

if [ "$TARGET_GOOS" = linux ]; then
  docker run --rm --pull=never --platform="$TARGET_GOOS/$TARGET_GOARCH" --network=none --read-only \
    --tmpfs /tmp:rw,exec,nosuid,size=1g,mode=1777 \
    --user "$(id -u):$(id -g)" \
    -v "$SNAPSHOT:/src:ro" \
    -w /src/integration \
    --entrypoint /usr/bin/env \
    "$NATIVE_HOST_IMAGE" \
    -i \
    PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    HOME=/tmp \
    TMPDIR=/tmp \
    CPA_BINARY="/src/.cache/native-host/$TARGET_GOOS/$TARGET_GOARCH/cli-proxy-api" \
    "/src/.cache/native-host/$TARGET_GOOS/$TARGET_GOARCH/integration.test" \
    -test.run "$TEST_SELECTOR" -test.count=1 -test.v
else
  (
    cd "$SNAPSHOT/integration"
    env -i \
      PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin \
      HOME="$TEMP_ROOT/home" \
      TMPDIR="$TEMP_ROOT/tmp" \
      CPA_BINARY="$SNAPSHOT/.cache/native-host/$TARGET_GOOS/$TARGET_GOARCH/cli-proxy-api" \
      "$SANDBOX_EXEC" -p "$SANDBOX_PROFILE" \
      "$SNAPSHOT/.cache/native-host/$TARGET_GOOS/$TARGET_GOARCH/integration.test" \
      -test.run "$TEST_SELECTOR" -test.count=1 -test.v
  )
fi
