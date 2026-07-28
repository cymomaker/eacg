GO ?= go
BINARY := bin/eacg-example
PACKAGES := ./...

.PHONY: all fmt vet test test-race cover build run run-api-key token clean docker-build

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
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/eacg-example

run:
	$(GO) run ./cmd/eacg-example

run-api-key:
	EACG_AUTH_MODE=api_key \
	EACG_API_KEY=0123456789abcdef0123456789abcdef \
	$(GO) run ./cmd/eacg-example

token:
	$(GO) run ./cmd/eacg-token

clean:
	$(GO) clean -testcache
	rm -f $(BINARY) coverage.out

docker-build:
	docker build -t eacg-example:local .
