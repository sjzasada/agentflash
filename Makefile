.PHONY: build build-all build-linux run test clean

BIN     := bin/agentflash
DIR     ?= $(PWD)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -ldflags "-s -w -X main.version=$(VERSION)"

build:
	go build $(LDFLAGS) -o $(BIN) .

# Cross-compile Linux binaries (CGO disabled for cross-compilation).
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/agentflash.linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o bin/agentflash.linux-arm64 .

# Verify both OSes still compile from the current host.
build-all: build build-linux

test:
	go test ./...

run: build
	./$(BIN) --dir $(DIR)

clean:
	rm -rf bin
