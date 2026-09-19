.PHONY: build build-faces install clean test test-fast test-smoke lint lint-tools fmt tidy jev-record help \
	build-linux-amd64 build-linux-arm64 \
	build-darwin-amd64 build-darwin-arm64 \
	build-all

# Binary name
BINARY_NAME=codeeagle
# Version (can be overridden)
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT?=$(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_DATE?=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
# Build directory
BUILD_DIR=bin

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod
GOFMT=gofmt

# pkg/jev is a separate module (see go.work), so the root's ./... no longer
# reaches it: every target that walks the tree names both.
PACKAGES=./... ./pkg/jev/...

# --- Linter ---
# golangci-lint type-checks against the standard library's export data, so the
# linter binary must be built with a Go toolchain at least as new as the one
# compiling this module — otherwise it panics with
# "file requires newer Go version goX.Y (application built with goX.Z)".
# We therefore install a pinned version *from source* with the local toolchain
# instead of relying on whatever prebuilt binary happens to be on PATH.
GOLANGCI_LINT_VERSION?=v2.13.2
GOBIN_DIR:=$(shell go env GOBIN)
ifeq ($(GOBIN_DIR),)
GOBIN_DIR:=$(shell go env GOPATH)/bin
endif
GOLANGCI_LINT=$(GOBIN_DIR)/golangci-lint
# Export-data compatibility is tied to the Go minor version (e.g. "go1.27").
GO_MINOR:=$(shell go env GOVERSION | cut -d. -f1-2)

# Build flags — inject version info via ldflags
LDFLAGS=-ldflags "-s -w \
  -X github.com/imyousuf/CodeEagle/internal/cli.Version=$(VERSION) \
  -X github.com/imyousuf/CodeEagle/internal/cli.Commit=$(COMMIT) \
  -X github.com/imyousuf/CodeEagle/internal/cli.BuildDate=$(BUILD_DATE)"

# --- Auto-detect optional build tags ---
# faces: requires OpenCV 4 (libopencv-dev)
HAS_OPENCV := $(shell pkg-config --exists opencv4 2>/dev/null && echo 1)
# Accumulate tags for the smart build.
BUILD_TAGS :=
ifdef HAS_OPENCV
BUILD_TAGS += faces
endif

# Collapse to comma-free, space-separated tag string for -tags flag.
TAGS_FLAG := $(strip $(BUILD_TAGS))

# Default target
all: build

## build: Build the binary (auto-detects faces support)
build:
	@mkdir -p $(BUILD_DIR)
ifneq ($(TAGS_FLAG),)
	@echo "Detected build tags: $(TAGS_FLAG)"
	$(GOBUILD) -tags "$(TAGS_FLAG)" $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/codeeagle
else
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/codeeagle
endif

## build-minimal: Build without optional features (no faces)
build-minimal:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/codeeagle

## build-faces: Build with face detection support (requires libopencv-dev)
build-faces:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -tags faces $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/codeeagle

## build-info: Show detected optional dependencies
build-info:
	@echo "Optional dependency detection:"
	@echo "  OpenCV 4 (faces):   $(if $(HAS_OPENCV),YES,NO)"
	@echo ""
	@echo "Auto build tags: $(if $(TAGS_FLAG),$(TAGS_FLAG),(none))"

## install: Build and install to $GOPATH/bin (auto-detects faces support)
install:
ifneq ($(TAGS_FLAG),)
	@echo "Detected build tags: $(TAGS_FLAG)"
	$(GOCMD) install -tags "$(TAGS_FLAG)" $(LDFLAGS) ./cmd/codeeagle
else
	$(GOCMD) install $(LDFLAGS) ./cmd/codeeagle
endif

## build-linux-amd64: Build for Linux x86_64
build-linux-amd64:
	@mkdir -p $(BUILD_DIR)/linux-amd64
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/linux-amd64/$(BINARY_NAME) ./cmd/codeeagle
	tar -czf $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64.tar.gz -C $(BUILD_DIR)/linux-amd64 $(BINARY_NAME)
	@rm -rf $(BUILD_DIR)/linux-amd64

## build-linux-arm64: Build for Linux ARM64
build-linux-arm64:
	@mkdir -p $(BUILD_DIR)/linux-arm64
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/linux-arm64/$(BINARY_NAME) ./cmd/codeeagle
	tar -czf $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64.tar.gz -C $(BUILD_DIR)/linux-arm64 $(BINARY_NAME)
	@rm -rf $(BUILD_DIR)/linux-arm64

## build-darwin-amd64: Build for macOS x86_64
build-darwin-amd64:
	@mkdir -p $(BUILD_DIR)/darwin-amd64
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/darwin-amd64/$(BINARY_NAME) ./cmd/codeeagle
	tar -czf $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64.tar.gz -C $(BUILD_DIR)/darwin-amd64 $(BINARY_NAME)
	@rm -rf $(BUILD_DIR)/darwin-amd64

## build-darwin-arm64: Build for macOS ARM64 (Apple Silicon)
build-darwin-arm64:
	@mkdir -p $(BUILD_DIR)/darwin-arm64
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/darwin-arm64/$(BINARY_NAME) ./cmd/codeeagle
	tar -czf $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64.tar.gz -C $(BUILD_DIR)/darwin-arm64 $(BINARY_NAME)
	@rm -rf $(BUILD_DIR)/darwin-arm64

## build-all: Build for all platforms
build-all: build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-darwin-arm64
	@echo "Built archives for all platforms in $(BUILD_DIR)/"
	@ls -lh $(BUILD_DIR)/*.tar.gz 2>/dev/null || true

## clean: Clean build artifacts
clean:
	$(GOCLEAN)
	rm -rf $(BUILD_DIR)

## test: Run tests with race detector
test:
	$(GOTEST) -race -v $(PACKAGES)

## test-fast: Run tests without race detector
test-fast:
	$(GOTEST) -v $(PACKAGES)

## test-smoke: Run smoke tests requiring real LLM APIs
test-smoke:
	$(GOTEST) ./... -tags=llm_smoke -v -count=1 -timeout=120s

## lint: Run linter
lint: lint-tools
	$(GOLANGCI_LINT) run $(PACKAGES)

## lint-tools: Install the pinned golangci-lint, built with the local Go toolchain
lint-tools:
	@if ! $(GOLANGCI_LINT) version 2>/dev/null | grep -q "version $(GOLANGCI_LINT_VERSION:v%=%) built with $(GO_MINOR)"; then \
		echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION) built with $$(go env GOVERSION)..."; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	fi

## fmt: Format code
fmt:
	$(GOFMT) -s -w .

## tidy: Tidy and verify dependencies
tidy:
	$(GOMOD) tidy
	$(GOMOD) verify

## jev-record: Re-record pkg/jev's fixture corpus from the live Jev service (paid; GROUP=<name> narrows it)
jev-record:
	@if [ -z "$$TYPESAFE_API_KEY$$JEV_API_KEY$$JEV_KEYRING_ACCOUNT" ]; then \
		echo "jev-record: set TYPESAFE_API_KEY, or JEV_KEYRING_ACCOUNT to read the key from the keyring" >&2; exit 1; \
	fi
	python3 pkg/jev/testdata/record_corpus.py $(GROUP)
	cd pkg/jev && $(GOTEST) ./... -count=1

## help: Show this help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed 's/^/ /'
