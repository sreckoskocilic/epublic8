BIN := bin/epublic8

.PHONY: all build test lint clean install-tools

all: lint test build

build:
	go build -o $(BIN) ./cmd/server

test:
	go test ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

install-tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
