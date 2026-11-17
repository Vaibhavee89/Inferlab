.PHONY: run build test bench

run:
	go run ./cmd/server

build:
	go build -o bin/inferlab ./cmd/server

test:
	go vet ./...
	go test -race ./...

MODEL ?= llama3.2

bench:
	go run ./cmd/bench --model $(MODEL) --prompt-file benchmarks/prompts/short.txt --stream
