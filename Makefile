.PHONY: build build-faces build-app dev-app install clean test test-fast test-smoke lint fmt tidy help \
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

# Build flags — inject version info via ldflags
LDFLAGS=-ldflags "-s -w \
  -X github.com/imyousuf/CodeEagle/internal/cli.Version=$(VERSION) \
  -X github.com/imyousuf/CodeEagle/internal/cli.Commit=$(COMMIT) \
  -X github.com/imyousuf/CodeEagle/internal/cli.BuildDate=$(BUILD_DATE)"

# --- Auto-detect optional build tags ---
# faces: requires OpenCV 4 (libopencv-dev)
HAS_OPENCV := $(shell pkg-config --exists opencv4 2>/dev/null && echo 1)
# app: requires webkit2gtk (libwebkit2gtk-4.x-dev) + npm
HAS_WEBKIT := $(shell (pkg-config --exists webkit2gtk-4.0 2>/dev/null || pkg-config --exists webkit2gtk-4.1 2>/dev/null) && echo 1)
HAS_NPM := $(shell which npm >/dev/null 2>&1 && echo 1)
HAS_APP_DEPS := $(if $(and $(HAS_WEBKIT),$(HAS_NPM)),1,)

# Accumulate tags for the smart build.
BUILD_TAGS :=
ifdef HAS_OPENCV
BUILD_TAGS += faces
endif
ifdef HAS_APP_DEPS
BUILD_TAGS += app desktop production
endif

# Build webkit2_41 tag if 4.1 is present (Wails uses this to pick the right pkg-config).
HAS_WEBKIT_41 := $(shell pkg-config --exists webkit2gtk-4.1 2>/dev/null && echo 1)
ifdef HAS_WEBKIT_41
ifdef HAS_APP_DEPS
BUILD_TAGS += webkit2_41
endif
endif

# Collapse to comma-free, space-separated tag string for -tags flag.
TAGS_FLAG := $(strip $(BUILD_TAGS))

# Default target
all: build

## build: Build the binary (auto-detects faces & app support)
build:
	@mkdir -p $(BUILD_DIR)
ifneq ($(TAGS_FLAG),)
	@echo "Detected build tags: $(TAGS_FLAG)"
ifdef HAS_APP_DEPS
	@cd internal/app/frontend && npm install --silent && npm run build --silent
endif
	$(GOBUILD) -tags "$(TAGS_FLAG)" $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/codeeagle
else
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/codeeagle
endif

## build-minimal: Build without optional features (no faces, no app)
build-minimal:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/codeeagle

## build-faces: Build with face detection support (requires libopencv-dev)
build-faces:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -tags faces $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/codeeagle

## build-app: Build with desktop app support (requires webkit2gtk-dev + npm)
build-app:
	@mkdir -p $(BUILD_DIR)
	cd internal/app/frontend && npm install && npm run build
	$(GOBUILD) -tags "app desktop production" $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/codeeagle

## dev-app: Run desktop app in development mode with hot reload
dev-app:
	cd internal/app/frontend && npm install
	wails dev -tags app

## build-info: Show detected optional dependencies
build-info:
	@echo "Optional dependency detection:"
	@echo "  OpenCV 4 (faces):   $(if $(HAS_OPENCV),YES,NO)"
	@echo "  webkit2gtk (app):   $(if $(HAS_WEBKIT),YES ($(if $(HAS_WEBKIT_41),4.1,4.0)),NO)"
	@echo "  npm (app frontend): $(if $(HAS_NPM),YES,NO)"
	@echo "  App deps met:       $(if $(HAS_APP_DEPS),YES,NO)"
	@echo ""
	@echo "Auto build tags: $(if $(TAGS_FLAG),$(TAGS_FLAG),(none))"

## install: Build and install to $GOPATH/bin (auto-detects faces & app support)
install:
ifdef HAS_APP_DEPS
	@cd internal/app/frontend && npm install --silent && npm run build --silent
endif
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
	$(GOTEST) -race -v ./...

## test-fast: Run tests without race detector
test-fast:
	$(GOTEST) -v ./...

## test-smoke: Run smoke tests requiring real LLM APIs
test-smoke:
	$(GOTEST) ./... -tags=llm_smoke -v -count=1 -timeout=120s

## lint: Run linter
lint:
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run ./...

## fmt: Format code
fmt:
	$(GOFMT) -s -w .

## tidy: Tidy and verify dependencies
tidy:
	$(GOMOD) tidy
	$(GOMOD) verify

## help: Show this help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed 's/^/ /'
