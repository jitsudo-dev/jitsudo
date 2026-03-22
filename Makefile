BINARY_DIR   := bin
CLI_BINARY   := $(BINARY_DIR)/jitsudo
SERVER_BINARY := $(BINARY_DIR)/jitsudod
MODULE       := github.com/jitsudo-dev/jitsudo
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS      := -ldflags "-X $(MODULE)/internal/version.Version=$(VERSION)"

.PHONY: all build build-cli build-server test lint proto docker-up docker-down dev-deps dev-server clean help

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
# -p 1 runs packages sequentially. Integration tests share a single PostgreSQL
# instance, so parallel package execution causes cross-package audit log
# interference (the global hash chain is written by multiple packages at once).
test-integration:
	go test -p 1 ./... -tags integration -race -count=1

## test-e2e: Run end-to-end tests (requires live cloud credentials)
# -p 1 for the same reason as test-integration: shared external state.
test-e2e:
	go test -p 1 ./... -tags e2e -race -count=1

## lint: Run golangci-lint
lint:
	golangci-lint run ./...

## proto: Regenerate Go code from protobuf definitions (requires buf)
proto:
	buf dep update
	buf generate
	go mod tidy

## proto-lint: Lint protobuf definitions
proto-lint:
	buf lint

## proto-breaking: Check for breaking changes in protobuf definitions
proto-breaking:
	buf breaking --against '.git#branch=main'

## dev-deps: Start only PostgreSQL and dex (run jitsudod on the host with make dev-server)
dev-deps:
	docker compose -f deploy/docker-compose.yaml up -d postgres dex

## dev-server: Build and run jitsudod on the host against local docker deps
dev-server: build-server
	JITSUDOD_DATABASE_URL=postgres://jitsudo:jitsudo@localhost:5432/jitsudo?sslmode=disable \
	JITSUDOD_OIDC_ISSUER=http://localhost:5556/dex \
	JITSUDOD_OIDC_CLIENT_ID=jitsudo-cli \
	JITSUDOD_HTTP_ADDR=:8080 \
	JITSUDOD_GRPC_ADDR=:8443 \
	$(SERVER_BINARY)

## docker-up: Start full local environment in Docker (jitsudod + PostgreSQL + dex)
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
