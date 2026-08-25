MODULE := github.com/Chinsusu/vm-factory
BIN_DIR := bin
VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)

.PHONY: fmt lint vet test build db-up db-down migrate-up migrate-down lab-mocks clean

fmt:
	gofmt -l .
	goimports -l .

lint: fmt vet
	golangci-lint run

vet:
	go vet ./...

test:
	go test -race -coverprofile=coverage.out ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/vmf-api ./cmd/api
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/vmf-worker ./cmd/worker
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/vmf ./cmd/cli

db-up:
	docker compose -f deploy/docker-compose.yml up -d postgres

db-down:
	docker compose -f deploy/docker-compose.yml down

migrate-up:
	migrate -path migrations -database "$$DATABASE_URL" up

migrate-down:
	migrate -path migrations -database "$$DATABASE_URL" down 1

lab-mocks:
	@echo "Proxmox/PGW mock server chưa implement — epic P0-11 (Test Lab & Chaos)"

clean:
	rm -rf $(BIN_DIR) coverage.out
