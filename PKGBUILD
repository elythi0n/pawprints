# Maintainer: Marcos Raudkett (Elythion) <raudkett@gmail.com>
# Builds pawprints from the checked-out source tree: cd into the repo and run
# `makepkg -si`. The vsix is per user, so install it with install.sh or
# `code --install-extension`.
pkgname=pawprints
pkgver=0.1.0
pkgrel=1
pkgdesc="Version history of every file Claude Code touches"
arch=('x86_64')
url="https://github.com/elythi0n/pawprints"
license=('MIT')
depends=('git')
makedepends=('go')
options=('!lto')

pkgver() {
	cd "$startdir"
	local v
	v=$(git describe --tags --always 2>/dev/null | sed 's/^v//; s/-/./g')
	# Before the first commit or tag there is nothing to describe.
	echo "${v:-0.1.0}"
}

build() {
	cd "$startdir"
	export CGO_ENABLED=0
	export GOFLAGS="-trimpath -mod=readonly -modcacherw"
	version="v$(pkgver)"
	commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
	export GOFLAGS="$GOFLAGS -buildvcs=false"
	go build -ldflags "-w -s \
		-X github.com/elythi0n/pawprints/internal/buildinfo.Version=$version \
		-X github.com/elythi0n/pawprints/internal/buildinfo.Commit=$commit \
		-X github.com/elythi0n/pawprints/internal/buildinfo.BuildTime=$(date -u +%Y-%m-%dT%H:%MZ)" \
		-o "$srcdir/pawprints" ./cmd/pawprints
}

check() {
	cd "$startdir"
	# makepkg's own pkg/ and src/ dirs sit inside the tree, so name the packages.
	GOFLAGS="$GOFLAGS -buildvcs=false" go test ./internal/... ./cmd/... >/dev/null
}

package() {
	install -Dm755 "$srcdir/pawprints" "$pkgdir/usr/bin/pawprints"
	install -Dm644 "$startdir/LICENSE" "$pkgdir/usr/share/licenses/$pkgname/LICENSE"
	install -Dm644 "$startdir/README.md" "$pkgdir/usr/share/doc/$pkgname/README.md"
	install -Dm644 "$startdir/internal/cli/AGENTS.md" "$pkgdir/usr/share/doc/$pkgname/AGENTS.md"
	install -d "$pkgdir/usr/share/$pkgname"
	cp -r "$startdir/plugin" "$pkgdir/usr/share/$pkgname/plugin"
	"$srcdir/pawprints" completion bash > "$srcdir/pawprints.bash"
	"$srcdir/pawprints" completion zsh > "$srcdir/_pawprints"
	"$srcdir/pawprints" completion fish > "$srcdir/pawprints.fish"
	install -Dm644 "$srcdir/pawprints.bash" "$pkgdir/usr/share/bash-completion/completions/pawprints"
	install -Dm644 "$srcdir/_pawprints" "$pkgdir/usr/share/zsh/site-functions/_pawprints"
	install -Dm644 "$srcdir/pawprints.fish" "$pkgdir/usr/share/fish/vendor_completions.d/pawprints.fish"
}
