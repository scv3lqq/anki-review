BIN := bin/anki-review

.PHONY: check lint test build run

check: lint test

lint:
	golangci-lint run ./...

test:
	go test -race ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o $(BIN) ./cmd/anki-review

run:
	go run ./cmd/anki-review
