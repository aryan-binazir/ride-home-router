.PHONY: build build-all run clean test lint help

# Variables
MODULE := ride-home-router
MAIN_PKG := ./cmd/server
BIN_DIR := bin
GO := go

# Detect current OS/ARCH for native build
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

# Platform-specific binary name
ifeq ($(GOOS),windows)
	BIN_NAME := $(BIN_DIR)/$(MODULE)-$(GOOS)-$(GOARCH).exe
else
	BIN_NAME := $(BIN_DIR)/$(MODULE)-$(GOOS)-$(GOARCH)
endif

# Build information
LDFLAGS := -ldflags "-s -w"
VERSION ?= dev
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')

help:
	@echo "Ride Home Router - Build Targets"
	@echo ""
	@echo "  make build       Build for current platform"
	@echo "  make build-all   Build for all target platforms"
	@echo "  make run         Build and run locally"
	@echo "  make clean       Remove build artifacts"
	@echo "  make test        Run tests"
	@echo "  make lint        Run go vet"
	@echo ""

build: $(BIN_DIR)
	@echo "Building $(MODULE) for $(GOOS)/$(GOARCH)..."
	GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build $(LDFLAGS) -o $(BIN_NAME) $(MAIN_PKG)
	@echo "✓ Built: $(BIN_NAME)"

build-all: $(BIN_DIR) build-windows build-macos-amd64 build-macos-arm64
	@echo "✓ All builds complete"

build-windows:
	@echo "Building for Windows amd64..."
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(MODULE)-windows-amd64.exe $(MAIN_PKG)

build-macos-amd64:
	@echo "Building for macOS amd64..."
	GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(MODULE)-darwin-amd64 $(MAIN_PKG)

build-macos-arm64:
	@echo "Building for macOS arm64..."
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(MODULE)-darwin-arm64 $(MAIN_PKG)

run: build
	@echo "Running $(MODULE)..."
	@./$(BIN_NAME)

clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BIN_DIR)
	@echo "✓ Clean complete"

test:
	@echo "Running tests..."
	@$(GO) test -v -race -coverprofile=coverage.out ./...
	@echo "✓ Tests complete"

lint:
	@echo "Running go vet..."
	@$(GO) vet ./...
	@echo "✓ Lint complete"

$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

# Include current workspace directory in Go searches
.DEFAULT_GOAL := help
