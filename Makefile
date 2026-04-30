.PHONY: build run clean

BIN := bin/agentflash
DIR ?= $(PWD)

build:
	go build -o $(BIN) .

run: build
	./$(BIN) --dir $(DIR)

clean:
	rm -rf bin
