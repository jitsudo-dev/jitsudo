BINARY_DIR   := bin
CLI_BINARY   := $(BINARY_DIR)/jitsudo
SERVER_BINARY := $(BINARY_DIR)/jitsudod
MODULE       := github.com/jitsudo-dev/jitsudo
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS      := -ldflags "-X $(MODULE)/internal/version.Version=$(VERSION)"

.PHONY: all build build-cli build-server test lint proto docker-up docker-down clean help

all: build

## build: Build both jitsudo CLI and jitsudod server binaries
build: build-cli build-server

build-cli:
	@mkdir -p $(BINARY_DIR)
	go build $(LDFLAGS) -o $(CLI_BINARY) ./cmd/jitsudo

build-server:
	@mkdir -p $(BINARY_DIR)
	go build $(LDFLAGS) -o $(SERVER_BINARY) ./cmd/jitsudod

## test: Run unit tests (no external dependencies required)
test:
	go test ./... -short -race -count=1

## test-integration: Run integration tests (requires Docker)
test-integration:
	go test ./... -tags integration -race -count=1

## test-e2e: Run end-to-end tests (requires live cloud credentials)
test-e2e:
	go test ./... -tags e2e -race -count=1

## lint: Run golangci-lint
lint:
	golangci-lint run ./...

## proto: Regenerate Go code from protobuf definitions (requires buf)
proto:
	buf generate

## proto-lint: Lint protobuf definitions
proto-lint:
	buf lint

## proto-breaking: Check for breaking changes in protobuf definitions
proto-breaking:
	buf breaking --against '.git#branch=main'

## docker-up: Start local development environment (jitsudod + PostgreSQL + dex)
docker-up:
	docker compose -f deploy/docker-compose.yaml up -d

## docker-down: Stop local development environment
docker-down:
	docker compose -f deploy/docker-compose.yaml down

## docker-logs: Tail logs from local development environment
docker-logs:
	docker compose -f deploy/docker-compose.yaml logs -f

## clean: Remove build artifacts
clean:
	rm -rf $(BINARY_DIR)

## tidy: Run go mod tidy
tidy:
	go mod tidy

## help: Show this help
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
