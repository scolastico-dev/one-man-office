BINARY      := omo
PKG         := ./cmd/omo
PREFIX      ?= /usr/local
BINDIR      ?= $(PREFIX)/bin
BUILD_DIR   := bin
GOOS        ?= $(shell go env GOOS)
GOARCH      ?= $(shell go env GOARCH)
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

# omo embeds a pure-Go SQLite driver: never link against C.
export CGO_ENABLED := 0

.DEFAULT_GOAL := build

# NO_WEB=1 builds the Go binary without the browser app (no Node.js needed).
WEB_DEPS := $(if $(NO_WEB),,web)
.PHONY: build cross cross-guard cross-linux cross-darwin cross-windows web web-check e2e test install uninstall clean fmt vet lint tidy check help

## web: build the browser app into web/dist (needs Node.js 24 and npm)
web:
	@command -v npm >/dev/null 2>&1 || { echo "Node.js 24 and npm are needed to build the board; use NO_WEB=1 to build without it"; exit 1; }
	@if [ ! -f web/node_modules/.package-lock.json ] || [ web/package-lock.json -nt web/node_modules/.package-lock.json ]; then npm --prefix web ci; fi
	npm --prefix web run build

## web-check: type-check and unit-test the browser app
web-check:
	npm --prefix web run typecheck
	npm --prefix web run test

## e2e: build omo and run the browser end-to-end tests
e2e: build
	npm --prefix web run e2e

## build: compile the omo binary into ./bin (NO_WEB=1 skips the browser app)
build: $(WEB_DEPS)
	@mkdir -p $(BUILD_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)$(if $(filter windows,$(GOOS)),.exe) $(PKG)
	@echo "built $(BUILD_DIR)/$(BINARY)$(if $(filter windows,$(GOOS)),.exe) ($(GOOS)/$(GOARCH), $(VERSION))"

## cross: build Linux, macOS and Windows for amd64 and arm64 (NO_WEB=1 skips the browser app)
cross: cross-guard cross-linux cross-darwin cross-windows

cross-guard: $(WEB_DEPS)
	@if [ -z "$(NO_WEB)" ] && [ ! -f web/dist/index.html ]; then echo "web/dist/index.html is missing: build the board with 'make web' or use NO_WEB=1"; exit 1; fi


cross-linux: cross-guard
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-linux-amd64 $(PKG)
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-linux-arm64 $(PKG)

cross-darwin: cross-guard
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 $(PKG)
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 $(PKG)

cross-windows: cross-guard
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe $(PKG)
	GOOS=windows GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-windows-arm64.exe $(PKG)

## test: run the full suite (the smoke test builds omo and starts a real server)
test:
	go test ./... -timeout 900s

## check: everything CI should gate on
check: fmt vet test

## fmt: report unformatted files (does not rewrite)
fmt:
	@out=$$(find . -path ./web/node_modules -prune -o -name '*.go' -print | xargs gofmt -l); \
	if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; exit 1; fi

## vet: run go vet
vet:
	go vet ./...

## tidy: sync go.mod/go.sum
tidy:
	go mod tidy

## install: install omo to PREFIX/bin (default /usr/local, e.g. PREFIX=~/.local)
install: build
	@install -d $(DESTDIR)$(BINDIR)
	install -m 0755 $(BUILD_DIR)/$(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)
	@echo "installed $(DESTDIR)$(BINDIR)/$(BINARY)"
	@command -v $(BINARY) >/dev/null 2>&1 || \
		echo "note: $(BINDIR) is not on your PATH — agents invoke 'omo' by name, so add it"

## uninstall: remove the installed binary
uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)

## clean: remove build output
clean:
	rm -rf $(BUILD_DIR)

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
