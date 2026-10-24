BIN := bin/anki-review
BIN_LINUX := bin/anki-review-linux-amd64

# Optional local settings for `make run`, see .env.example.
-include .env
export

.PHONY: check lint test build build-linux run

check: lint test

lint:
	golangci-lint run ./...

test:
	go test -race ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o $(BIN) ./cmd/anki-review

# The binary for the deploy kit (deploy/README.md).
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o $(BIN_LINUX) ./cmd/anki-review

run:
	go run ./cmd/anki-review
