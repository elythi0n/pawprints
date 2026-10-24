#!/bin/sh
# Installs pawprints from its GitHub releases: the binary to ~/.local/bin,
# then `pawprints setup` to connect Claude Code, VS Code or Cursor, and the
# shell. POSIX sh so it runs under bash, dash, and BusyBox ash.
#
#   curl -fsSL https://github.com/elythi0n/pawprints/releases/latest/download/install.sh | sh
#
# Environment overrides:
#   PAWPRINTS_VERSION      tag to install, default: the latest published
#   PAWPRINTS_INSTALL_DIR  where the binary goes, default ~/.local/bin
#   PAWPRINTS_RELEASES_URL releases base, default https://github.com/elythi0n/pawprints/releases
#   PAWPRINTS_NO_SETUP     set to 1 to only place the binary
set -eu

BASE="${PAWPRINTS_RELEASES_URL:-https://github.com/elythi0n/pawprints/releases}"
BINARY=pawprints

die() { printf 'pawprints-install: %s\n' "$1" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }
have curl || die "need curl on PATH"

fetch_to() { curl -fsSL -o "$1" "$2"; }

case "$(uname -s)" in
	Linux) ;;
	*) die "pawprints is built for Linux only right now" ;;
esac
case "$(uname -m)" in
	x86_64|amd64) ;;
	*) die "pawprints is built for x86_64 only right now (have $(uname -m))" ;;
esac

version="${PAWPRINTS_VERSION:-}"
if [ -z "$version" ]; then
	version=$(curl -fsSL "$BASE/latest/download/VERSION" 2>/dev/null | head -n1 | tr -d '[:space:]') || true
	[ -n "$version" ] || die "could not resolve the latest version; set PAWPRINTS_VERSION=vX.Y.Z"
fi
case "$version" in v*) ;; *) version="v$version" ;; esac
bare=${version#v}
printf 'pawprints-install: installing %s\n' "$version"

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t pawprints)
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch_to "$tmp/$BINARY" "$BASE/download/$version/$BINARY" || die "download failed: $BASE/download/$version/$BINARY"
if fetch_to "$tmp/checksums.txt" "$BASE/download/$version/checksums.txt" 2>/dev/null && have sha256sum; then
	expected=$(awk -v f="$BINARY" '$2 == f { print $1 }' "$tmp/checksums.txt")
	actual=$(sha256sum "$tmp/$BINARY" | awk '{ print $1 }')
	[ -z "$expected" ] || [ "$expected" = "$actual" ] || die "checksum mismatch for $BINARY"
fi
chmod +x "$tmp/$BINARY"

dir="${PAWPRINTS_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$dir"
mv "$tmp/$BINARY" "$dir/$BINARY"
printf 'pawprints-install: binary -> %s\n' "$dir/$BINARY"
case ":$PATH:" in
	*":$dir:"*) ;;
	*) printf 'pawprints-install: %s is not on your PATH; add it to your shell rc\n' "$dir" ;;
esac

# The binary carries the editor extension and knows how to connect Claude
# Code and the shell, so it does the rest: a checklist when someone is at a
# terminal, the defaults otherwise.
if [ "${PAWPRINTS_NO_SETUP:-0}" != "1" ]; then
	if [ -t 1 ] && (exec </dev/tty) 2>/dev/null; then
		"$dir/$BINARY" setup </dev/tty || true
	else
		"$dir/$BINARY" setup --yes || true
	fi
fi

cat <<MSG

Run setup again any time:  $dir/$BINARY setup
Check everything with:    $dir/$BINARY doctor

Prefer the Claude Code plugin to settings.json hooks? It works alongside:
  /plugin marketplace add elythi0n/pawprints
  /plugin install pawprints@pawprints
MSG
