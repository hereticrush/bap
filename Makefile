# ──────────────────────────────────────────────
# bap — Makefile
#
# Usage: make <target>
# Run 'make help' for available targets.
#
# Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
# ──────────────────────────────────────────────

# Binary name and source path
BINARY   := bap
CMD_PATH := ./cmd/main.go

# Version injection via ldflags
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE     := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || echo "unknown")
LDFLAGS  := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Docker image coordinates
DOCKER_IMAGE := ghcr.io/hereticrush/bap
DOCKER_TAG   := latest

# Go environment
export CGO_ENABLED := 1

# ──────────────────────────────────────────────
# Targets
# ──────────────────────────────────────────────

.PHONY: all build test lint fmt vet deps clean \
        docker-build docker-up docker-down docker-test docker-deps \
        version help

all: lint build ## Run lint then build (default)

build: ## Build the bap binary with version injection
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD_PATH)

test: ## Run the full test suite with race detection
	go test -v -race ./...

lint: ## Run golangci-lint static analysis
	golangci-lint run ./...

fmt: ## Format all Go source files and run go vet
	gofmt -w .
	go vet ./...

vet: ## Run go vet only
	go vet ./...

deps: ## Tidy and download Go module dependencies
	go mod tidy
	go mod download

clean: ## Remove compiled binary and temporary artifacts
	rm -f $(BINARY)
	rm -rf tmp/

# ──────────────────────────────────────────────
# Docker targets
# ──────────────────────────────────────────────

docker-build: ## Build Docker image
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

docker-up: ## Start all services (app + redis) via Docker Compose
	docker-compose up -d --build

docker-down: ## Stop all services
	docker-compose down

docker-test: ## Run tests inside a Docker container (CGO + Alpine)
	docker run --rm \
	  -v "$$(pwd)":/src \
	  -w /src \
	  golang:1.25-alpine \
	  sh -c "apk add --no-cache gcc musl-dev > /dev/null && go mod download && CGO_ENABLED=1 go test -v ./..."

docker-deps: ## Fetch and tidy dependencies inside a Docker container
	docker run --rm \
	  -v "$$(pwd)":/src \
	  -w /src \
	  golang:1.25-alpine \
	  sh -c "apk add --no-cache gcc musl-dev > /dev/null && go mod tidy && go mod download"

# ──────────────────────────────────────────────
# Informational targets
# ──────────────────────────────────────────────

version: ## Print the version that would be injected into the binary
	@echo "Version: $(VERSION)"
	@echo "Commit:  $(COMMIT)"
	@echo "Date:    $(DATE)"

help: ## Show this help message
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
