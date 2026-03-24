BIN := bin/epublic8

# Build variables - can be overridden at build time
VERSION ?= dev
COMMIT ?= unknown
BUILD_TIME ?= unknown

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
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
