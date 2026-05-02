.PHONY: all build test lint eval clean tidy

BINARY_AGENT   := bin/agent
BINARY_INDEXER := bin/indexer
BINARY_CLI     := bin/cli

all: build

build:
	@mkdir -p bin
	go build -o $(BINARY_AGENT)   ./cmd/agent
	go build -o $(BINARY_INDEXER) ./cmd/indexer
	go build -o $(BINARY_CLI)     ./cmd/cli

test:
	go test ./... -count=1 -timeout 120s

test-integration:
	go test ./... -count=1 -timeout 300s -tags integration

lint:
	@which golangci-lint > /dev/null || (echo "install golangci-lint first" && exit 1)
	golangci-lint run ./...

eval:
	go run ./eval/replay/...

tidy:
	go mod tidy

clean:
	rm -rf bin

# Run the CLI locally (dev mode with local sandbox)
process-ticket:
	go run ./cmd/cli process-ticket $(TICKET)

.PHONY: migrate-up migrate-down
migrate-up:
	GOOSE_DRIVER=postgres GOOSE_DBSTRING="$(DATABASE_URL)" goose -dir pkg/store/migrations up

migrate-down:
	GOOSE_DRIVER=postgres GOOSE_DBSTRING="$(DATABASE_URL)" goose -dir pkg/store/migrations down
