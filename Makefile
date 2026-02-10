.PHONY: build install clean test test-fast test-smoke lint fmt tidy help \
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

# Default target
all: build

## build: Build the binary
build:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/codeeagle

## install: Build and install to $GOPATH/bin
install:
	$(GOCMD) install $(LDFLAGS) ./cmd/codeeagle

## build-linux-amd64: Build for Linux x86_64
build-linux-amd64:
	@mkdir -p $(BUILD_DIR)/linux-amd64
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/linux-amd64/$(BINARY_NAME) ./cmd/codeeagle
	tar -czf $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64.tar.gz -C $(BUILD_DIR)/linux-amd64 $(BINARY_NAME)
	@rm -rf $(BUILD_DIR)/linux-amd64

## build-linux-arm64: Build for Linux ARM64
build-linux-arm64:
	@mkdir -p $(BUILD_DIR)/linux-arm64
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/linux-arm64/$(BINARY_NAME) ./cmd/codeeagle
	tar -czf $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64.tar.gz -C $(BUILD_DIR)/linux-arm64 $(BINARY_NAME)
	@rm -rf $(BUILD_DIR)/linux-arm64

## build-darwin-amd64: Build for macOS x86_64
build-darwin-amd64:
	@mkdir -p $(BUILD_DIR)/darwin-amd64
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/darwin-amd64/$(BINARY_NAME) ./cmd/codeeagle
	tar -czf $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64.tar.gz -C $(BUILD_DIR)/darwin-amd64 $(BINARY_NAME)
	@rm -rf $(BUILD_DIR)/darwin-amd64

## build-darwin-arm64: Build for macOS ARM64 (Apple Silicon)
build-darwin-arm64:
	@mkdir -p $(BUILD_DIR)/darwin-arm64
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/darwin-arm64/$(BINARY_NAME) ./cmd/codeeagle
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
