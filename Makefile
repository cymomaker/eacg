GO ?= go
SERVER_BINARY := bin/eacg-example
CLIENT_BINARY := bin/eacg-client
PACKAGES := ./...

.PHONY: all fmt vet test test-race cover build run run-api-key client client-api-key token clean docker-build

all: fmt vet test build

fmt:
	$(GO) fmt $(PACKAGES)

vet:
	$(GO) vet $(PACKAGES)

test:
	$(GO) test $(PACKAGES)

test-race:
	$(GO) test -race $(PACKAGES)

cover:
	$(GO) test -coverprofile=coverage.out $(PACKAGES)
	$(GO) tool cover -func=coverage.out

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(SERVER_BINARY) ./cmd/eacg-example
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(CLIENT_BINARY) ./cmd/eacg-client

run:
	$(GO) run ./cmd/eacg-example

run-api-key:
	EACG_AUTH_MODE=api_key \
	EACG_API_KEY=0123456789abcdef0123456789abcdef \
	$(GO) run ./cmd/eacg-example

client:
	EACG_CLIENT_TOKEN="$$( $(GO) run ./cmd/eacg-token )" \
	$(GO) run ./cmd/eacg-client

client-api-key:
	EACG_CLIENT_AUTH_MODE=api_key \
	EACG_CLIENT_API_KEY=0123456789abcdef0123456789abcdef \
	$(GO) run ./cmd/eacg-client

token:
	$(GO) run ./cmd/eacg-token

clean:
	$(GO) clean -testcache
	rm -f $(SERVER_BINARY) $(CLIENT_BINARY) coverage.out

docker-build:
	docker build -t eacg-example:local .
