BINARY      := pawprints
MODULE      := github.com/elythi0n/pawprints
REPO        ?= elythi0n/pawprints
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME  := $(shell date -u +%Y-%m-%dT%H:%MZ)
RELEASES    := https://github.com/$(REPO)/releases

STAMP       := -X $(MODULE)/internal/buildinfo.Version=$(VERSION) \
               -X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) \
               -X $(MODULE)/internal/buildinfo.BuildTime=$(BUILD_TIME)
LDFLAGS     := -w -s $(STAMP)
BUILD_FLAGS := -trimpath -ldflags="$(LDFLAGS)"

VSIX        := bin/$(BINARY)-$(patsubst v%,%,$(VERSION)).vsix
EMBED_VSIX  := internal/setup/assets/pawprints.vsix
NOTES_FILE  ?=

.PHONY: all build run test lint check fmt install-user release vsix publish publish-only clean help

## build: fast local build → bin/pawprints
build:
	@mkdir -p bin
	go build $(BUILD_FLAGS) -o bin/$(BINARY) ./cmd/$(BINARY)
	@echo "bin/$(BINARY) $(VERSION)"

## run: build and open the terminal UI
run: build
	./bin/$(BINARY)

## test: unit tests with the race detector
test:
	go test -race -count=1 ./...

## lint: vet, staticcheck, golangci-lint
lint:
	go vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo "staticcheck not installed: go install honnef.co/go/tools/cmd/staticcheck@latest"; fi
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; else echo "golangci-lint not installed"; fi

## check: everything that must pass before pushing or tagging
check: lint test
	cd extension && npm ci --no-audit --no-fund >/dev/null && npm run lint && npm test && npm run build

## fmt: gofmt and goimports
fmt:
	gofmt -w .
	@if command -v goimports >/dev/null 2>&1; then goimports -w -local $(MODULE) .; fi

## install-user: install bin/pawprints to ~/.local/bin
install-user: build
	install -Dm755 bin/$(BINARY) $(HOME)/.local/bin/$(BINARY)
	@echo "installed: $(HOME)/.local/bin/$(BINARY)"
	@case ":$$PATH:" in *":$(HOME)/.local/bin:"*) ;; *) echo "warning: $(HOME)/.local/bin is not on PATH";; esac

## release: static linux/amd64 binary carrying the editor extension, the exact artifact that gets published
release: vsix
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(BUILD_FLAGS) -o bin/$(BINARY) ./cmd/$(BINARY)
	@cd bin && sha256sum $(BINARY) $(notdir $(VSIX)) > checksums.txt
	@echo "bin/$(BINARY) ($(VERSION), $(COMMIT), $(BUILD_TIME)) with $(VSIX) inside"

## vsix: stamp the version into the extension, package it to bin/, and place it where the binary embeds it
vsix:
	@mkdir -p bin
	scripts/stamp-version.sh $(VERSION)
	cd extension && npm ci --no-audit --no-fund >/dev/null && npm run build && npx vsce package --no-dependencies --out ../$(VSIX)
	cp $(VSIX) $(EMBED_VSIX)
	@echo "$(VSIX)"

## publish: release (which builds the vsix), then create the GitHub release with binary, vsix, checksums, install.sh and VERSION
publish: release
	@$(MAKE) --no-print-directory publish-only VERSION=$(VERSION)

## publish-only: upload already-built bin/ artifacts without rebuilding (needs gh, signed in)
publish-only:
	@if [ ! -f bin/$(BINARY) ]; then echo "bin/$(BINARY) not found; run make release"; exit 1; fi
	@case "$(VERSION)" in *-dirty|dev|*-g*) echo "error: version '$(VERSION)' is not a clean tag; commit and tag first, or set VERSION="; exit 1;; esac
	@command -v gh >/dev/null 2>&1 || { echo "error: the GitHub CLI (gh) is not installed"; exit 1; }
	@if gh release view $(VERSION) --repo $(REPO) >/dev/null 2>&1; then echo "error: $(VERSION) is already released"; exit 1; fi
	@# install.sh finds the newest version through releases/latest/download/VERSION.
	@printf '%s\n' "$(VERSION)" > bin/VERSION
	@echo "publishing $(BINARY) $(VERSION) to $(REPO)..."
	gh release create $(VERSION) --repo $(REPO) --verify-tag --title $(VERSION) $(if $(NOTES_FILE),--notes-file $(NOTES_FILE),--generate-notes) \
		bin/$(BINARY) $(VSIX) bin/checksums.txt install.sh bin/VERSION
	@echo ""
	@echo "published: $(RELEASES)/tag/$(VERSION)"
	@echo "install:   curl -fsSL $(RELEASES)/latest/download/install.sh | sh"

## clean: remove build artifacts
clean:
	rm -rf bin/ extension/dist/ extension/out/

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | awk -F: '{ printf "  %-14s %s\n", $$1, $$2 }'
