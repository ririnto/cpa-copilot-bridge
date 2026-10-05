#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
HOST_MODULE=github.com/router-for-me/CLIProxyAPI/v8
GOOS=$(GOENV=off GOWORK=off go env GOOS)
GOARCH=$(GOENV=off GOWORK=off go env GOARCH)

case "$GOOS" in
  linux) PLUGIN_EXT=so ;;
  darwin) PLUGIN_EXT=dylib ;;
  *)
    printf 'error: native host tests require a Linux or macOS plugin artifact\n' >&2
    exit 1
    ;;
esac

PLUGIN="$REPO_DIR/build/plugins/$GOOS/$GOARCH/cliproxyapi-copilot.$PLUGIN_EXT"
if [ ! -f "$PLUGIN" ]; then
	printf 'error: native plugin artifact is missing.\n' >&2
	printf 'Build the plugin before running native host tests.\n' >&2
	exit 1
fi

HOST_MODULE_DIR=$(
  cd "$REPO_DIR"
  GOENV=off GOWORK=off go list -mod=readonly -m -f '{{.Dir}}' "$HOST_MODULE"
)
if [ ! -f "$HOST_MODULE_DIR/cmd/server/main.go" ]; then
  printf 'error: CLIProxyAPI server source is missing from the selected module\n' >&2
  exit 1
fi

TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/cliproxyapi-native-host.XXXXXX")
trap 'rm -rf "$TEMP_DIR"' 0
trap 'exit 1' HUP INT TERM
CPA_BINARY="$TEMP_DIR/cli-proxy-api"

printf 'Building CLIProxyAPI v8 host from the module version selected by go.mod\n'
(
  cd "$HOST_MODULE_DIR"
  GOENV=off GOWORK=off go build -mod=readonly -buildvcs=false -o "$CPA_BINARY" ./cmd/server
  cd "$REPO_DIR"
  # The host-fork Codex WebSocket compaction test runs in a separate native lane.
  GOENV=off GOWORK=off CPA_BINARY="$CPA_BINARY" go test -mod=readonly ./integration -run '^(TestNativeHostProtocolRoundTrips|TestNativeHostOAuth.*|TestFilteredChildEnvironment)$' -count=1 -v
)
