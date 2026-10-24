#!/bin/sh
# Finds the pawprints binary and runs it with the given arguments.
#
# Order: PATH, then the plugin data directory, then a one-time download of the
# version pinned in plugin.json from the GitHub releases.
# Everything here is on Claude's hot path, so the common case is one exec.
#
# Environment:
#   PAWPRINTS_BIN           use this binary and skip every lookup
#   PAWPRINTS_RELEASES_URL  releases base, default https://github.com/elythi0n/pawprints/releases
#   CLAUDE_PLUGIN_ROOT      set by Claude Code; where plugin.json lives
#   CLAUDE_PLUGIN_DATA      set by Claude Code; survives plugin updates
set -u

# Hooks registered in settings.json step aside while the plugin is active;
# this tells the binary the call is the plugin's own.
export PAWPRINTS_VIA_PLUGIN=1

if [ -n "${PAWPRINTS_BIN:-}" ] && [ -x "$PAWPRINTS_BIN" ]; then
	exec "$PAWPRINTS_BIN" "$@"
fi
if command -v pawprints >/dev/null 2>&1; then
	exec pawprints "$@"
fi

data="${CLAUDE_PLUGIN_DATA:-$HOME/.claude/plugins/data/pawprints}"
bin="$data/bin/pawprints"
if [ -x "$bin" ]; then
	exec "$bin" "$@"
fi

# No binary anywhere: fetch the version this plugin was released with.
root="${CLAUDE_PLUGIN_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
version=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$root/.claude-plugin/plugin.json" | head -n1)
case "$version" in
	v*) ;;
	"") echo "pawprints: cannot read version from plugin.json" >&2; exit 0 ;;
	*) version="v$version" ;;
esac
base="${PAWPRINTS_RELEASES_URL:-https://github.com/elythi0n/pawprints/releases}/download/$version"

if ! command -v curl >/dev/null 2>&1; then
	echo "pawprints: binary not found and curl is missing; run install.sh or set PAWPRINTS_BIN" >&2
	exit 0
fi
mkdir -p "$data/bin" 2>/dev/null || exit 0
tmp="$data/bin/.pawprints.download"
if ! curl -fsSL -o "$tmp" "$base/pawprints"; then
	echo "pawprints: download of $version failed; run install.sh or set PAWPRINTS_BIN" >&2
	rm -f "$tmp"
	exit 0
fi
if curl -fsSL -o "$tmp.sums" "$base/checksums.txt" 2>/dev/null && command -v sha256sum >/dev/null 2>&1; then
	expected=$(awk '$2 == "pawprints" { print $1 }' "$tmp.sums")
	actual=$(sha256sum "$tmp" | awk '{ print $1 }')
	rm -f "$tmp.sums"
	if [ -n "$expected" ] && [ "$expected" != "$actual" ]; then
		echo "pawprints: checksum mismatch for $version; refusing to run it" >&2
		rm -f "$tmp"
		exit 0
	fi
fi
chmod +x "$tmp" && mv "$tmp" "$bin" && exec "$bin" "$@"
exit 0
