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
.PHONY: build cross cross-linux cross-darwin cross-windows test install uninstall clean run fmt vet lint tidy check help

## build: compile the omo binary into ./bin
build:
	@mkdir -p $(BUILD_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)$(if $(filter windows,$(GOOS)),.exe) $(PKG)
	@echo "built $(BUILD_DIR)/$(BINARY)$(if $(filter windows,$(GOOS)),.exe) ($(GOOS)/$(GOARCH), $(VERSION))"

## cross: build Linux, macOS and Windows for amd64 and arm64
cross: cross-linux cross-darwin cross-windows

cross-linux:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-linux-amd64 $(PKG)
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-linux-arm64 $(PKG)

cross-darwin:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 $(PKG)
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 $(PKG)

cross-windows:
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe $(PKG)
	GOOS=windows GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-windows-arm64.exe $(PKG)

## test: run the full suite (integration tests spawn real PTYs)
test:
	go test ./... -timeout 900s

## check: everything CI should gate on
check: fmt vet test

## fmt: report unformatted files (does not rewrite)
fmt:
	@out=$$(gofmt -l . ); \
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

## run: start an office in the current directory (needs .omo/omo.yaml here)
run: build
	./$(BUILD_DIR)/$(BINARY)

## demo: scaffold and run a throwaway mock office in ./tmp-demo
demo: build
	@rm -rf tmp-demo tmp-demo-repo
	@mkdir -p tmp-demo-repo && git -C tmp-demo-repo init -q -b main && \
		git -C tmp-demo-repo commit -q --allow-empty -m init
	./$(BUILD_DIR)/$(BINARY) setup tmp-demo >/dev/null
	@sed -i 's|^repos: {}|repos:\n  demo: $(CURDIR)/tmp-demo-repo|' tmp-demo/.omo/omo.yaml
	cd tmp-demo && ../$(BUILD_DIR)/$(BINARY) --mock

## clean: remove build output
clean:
	rm -rf $(BUILD_DIR)

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
