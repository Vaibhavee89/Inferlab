.PHONY: run build test

run:
	go run ./cmd/server

build:
	go build -o bin/inferlab ./cmd/server

test:
	go vet ./...
	go test -race ./...
