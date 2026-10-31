BIN := bin/anki-review
BIN_LINUX := bin/anki-review-linux-amd64

# Optional local settings for `make run`, see .env.example.
-include .env
export

.PHONY: check check-deploy lint test build build-linux run

check: lint test check-deploy

check-deploy:
	@set -e; for script in deploy/*.sh deploy/anki/*.sh .agents/skills/setup-anki-review/*.sh .claude/skills/setup-anki-review/*.sh; do \
		bash -n "$$script"; \
	done
	bash deploy/test-provider.sh

lint:
	golangci-lint run ./...

test:
	go test -race ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o $(BIN) ./cmd/anki-review

# The binary for the deploy kit (deploy/README.md). No VCS stamp: deploy/autodeploy.sh builds the
# same flags on the host and restarts only when the bytes differ.
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags='-s -w' -o $(BIN_LINUX) ./cmd/anki-review

run:
	go run ./cmd/anki-review
