#!/bin/sh
# Writes a version into plugin/.claude-plugin/plugin.json and
# extension/package.json so every part of a release carries the same number.
#
#   scripts/stamp-version.sh v0.3.0
#
# Idempotent. Accepts the tag with or without the leading v; the files get
# the bare number, which is what plugin.json and npm expect.
set -eu
cd "$(dirname "$0")/.."

version="${1:-}"
if [ -z "$version" ]; then
	echo "usage: $0 vX.Y.Z" >&2
	exit 64
fi
bare=${version#v}
case "$bare" in
	*[!0-9.]*|"") echo "not a release version: $version (pre-release or dirty builds are not stamped)" >&2; exit 0 ;;
esac

stamp() {
	file="$1"
	tmp="$file.tmp"
	sed "s/\"version\"[[:space:]]*:[[:space:]]*\"[^\"]*\"/\"version\": \"$bare\"/" "$file" > "$tmp"
	if cmp -s "$file" "$tmp"; then
		rm -f "$tmp"
		echo "$file already at $bare"
	else
		mv "$tmp" "$file"
		echo "$file -> $bare"
	fi
}

stamp plugin/.claude-plugin/plugin.json
[ -f extension/package.json ] && stamp extension/package.json
exit 0
