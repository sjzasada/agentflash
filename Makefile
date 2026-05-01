.PHONY: build build-all build-linux run test clean

BIN := bin/agentflash
DIR ?= $(PWD)

build:
	go build -o $(BIN) .

# Cross-compile a Linux binary from the macOS dev host.
build-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/agentflash.linux-amd64 .
	GOOS=linux GOARCH=arm64 go build -o bin/agentflash.linux-arm64 .

# Verify both OSes still compile from the current host.
build-all: build build-linux

test:
	go test ./...

run: build
	./$(BIN) --dir $(DIR)

clean:
	rm -rf bin
