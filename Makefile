BIN := bin/epublic8

# Build variables - can be overridden at build time
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "-X epublic8/internal/handler.Version=$(VERSION) -X epublic8/internal/handler.Commit=$(COMMIT) -X epublic8/internal/handler.BuildTime=$(BUILD_TIME)"

.PHONY: all build test lint clean install-tools

all: lint test build

build:
	go build $(LDFLAGS) -o $(BIN) ./cmd/server

test:
	go test ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

install-tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
