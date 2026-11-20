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
.PHONY: build relay relay-image notices cross cross-guard cross-linux cross-darwin cross-windows web web-check e2e stress stress-check readme-art readme-shots test install uninstall clean fmt vet lint tidy check help

## web: build the browser app into web/dist (needs Node.js 24 and npm)
web:
	@command -v npm >/dev/null 2>&1 || { echo "Node.js 24 and npm are needed to build the board; use NO_WEB=1 to build without it"; exit 1; }
	@if [ ! -f web/node_modules/.package-lock.json ] || [ web/package-lock.json -nt web/node_modules/.package-lock.json ]; then npm --prefix web ci; fi
	npm --prefix web run build

## web-check: type-check and unit-test the browser app
web-check:
	@failed=""; \
	for script in typecheck test; do \
		npm --prefix web run $$script || failed="$$failed $$script"; \
	done; \
	if [ -n "$$failed" ]; then echo "web-check: failed:$$failed"; exit 1; fi

## e2e: build omo and run the browser end-to-end tests (E2E_ARGS passes options to Playwright, such as --shard=1/4)
e2e: build
	npm --prefix web run e2e$(if $(E2E_ARGS), -- $(E2E_ARGS))

## stress: Linux two-CPU race/throttled-browser campaign (20 Go + 10 browser attempts; STRESS_ARGS for calibration)
stress:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/stress/run.py $(STRESS_ARGS)

## stress-check: check the Linux runner's accounting, isolation and process cleanup
stress-check:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/stress -p 'test_*.py'
	go test ./internal/testguard -count=1

## readme-art: render the README graphics in .github/assets/src to SVG (needs Playwright's Chromium and pdftocairo)
readme-art:
	@if [ ! -f web/node_modules/.package-lock.json ] || [ web/package-lock.json -nt web/node_modules/.package-lock.json ]; then npm --prefix web ci; fi
	npm --prefix web run readme-art

## readme-shots: build omo and take the README screenshots (.github/assets/screenshot-*.png) on a temporary home with scripted agents (needs Playwright's Chromium)
readme-shots: build
	npm --prefix web run readme-shots

## build: compile the omo binary into ./bin (NO_WEB=1 skips the browser app)
build: $(WEB_DEPS)
	@mkdir -p $(BUILD_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)$(if $(filter windows,$(GOOS)),.exe) $(PKG)
	@echo "built $(BUILD_DIR)/$(BINARY)$(if $(filter windows,$(GOOS)),.exe) ($(GOOS)/$(GOARCH), $(VERSION))"

## notices: write bin/THIRD_PARTY_NOTICES, the licences of the Go modules and the bundled npm packages (NO_WEB=1 leaves the web app out)
notices: $(WEB_DEPS)
	@mkdir -p $(BUILD_DIR)
	go run ./tools/notices -o $(BUILD_DIR)/THIRD_PARTY_NOTICES $(if $(NO_WEB),-web '')

## relay: build standalone bin/omo-relay and its linked-code notices (no Node.js)
relay:
	@mkdir -p $(BUILD_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/omo-relay$(if $(filter windows,$(GOOS)),.exe) ./cmd/omo-relay
	@set -eu; { \
		printf 'omo-relay includes the Go standard library and github.com/coder/websocket.\nSee LICENSE and NOTICE for omo-relay itself.\n\nGo standard library %s\n' "$$(go env GOVERSION)"; \
		cat "$$(go env GOROOT)/LICENSE" "$$(go env GOROOT)/PATENTS"; \
		printf '\n\n%s\n' "$$(go list -m github.com/coder/websocket)"; \
		cat "$$(go list -m -f '{{.Dir}}' github.com/coder/websocket)/LICENSE.txt"; \
	} > $(BUILD_DIR)/RELAY_THIRD_PARTY_NOTICES

## relay-image: build the separate standalone relay image (RELAY_IMAGE overrides tag)
RELAY_IMAGE ?= omo-relay:local
relay-image:
	docker build -f docker/relay.Dockerfile --build-arg VERSION='$(VERSION)' -t $(RELAY_IMAGE) .

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

## check: everything CI should gate on. Every stage runs even when an earlier
## one failed, so that one run reports all problems; the result is a failure
## when any stage failed.
check:
	@failed=""; \
	for stage in fmt vet lint test; do \
		echo "== make $$stage"; \
		$(MAKE) --no-print-directory $$stage || failed="$$failed $$stage"; \
	done; \
	if [ -n "$$failed" ]; then echo "== make check: failed stages:$$failed"; exit 1; fi; \
	echo "== make check: all stages passed"

## fmt: report unformatted files (does not rewrite)
fmt:
	@out=$$(find . -path ./web/node_modules -prune -o -name '*.go' -print | xargs gofmt -l); \
	if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; exit 1; fi

## vet: run go vet for this OS and type-check the Windows and macOS builds, tests included
vet:
	@failed=""; \
	for os in "" windows darwin; do \
		if [ -n "$$os" ]; then echo "GOOS=$$os go vet ./..."; else echo "go vet ./..."; fi; \
		GOOS=$$os go vet ./... || failed="$$failed $${os:-host}"; \
	done; \
	if [ -n "$$failed" ]; then echo "go vet failed for:$$failed"; exit 1; fi

## lint: report unused code with staticcheck when it is installed (skipped otherwise)
lint:
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck -checks U1000 ./...; else echo "staticcheck is not installed: unused-code check skipped"; fi

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
