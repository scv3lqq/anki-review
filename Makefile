BIN := bin/anki-review
BIN_LINUX := bin/anki-review-linux-amd64
BIN_PROVISIONER_LINUX := bin/anki-review-provisioner-linux-amd64

# Optional local settings for `make run`, see .env.example.
-include .env
export

.PHONY: check check-deploy check-web lint test build build-linux build-provisioner-linux run

check: lint test check-deploy check-web

check-deploy:
	@set -e; for script in deploy/*.sh deploy/anki/*.sh .agents/skills/setup-anki-review/*.sh .claude/skills/setup-anki-review/*.sh; do \
		bash -n "$$script"; \
	done
	bash deploy/test-provider.sh
	python3 -I deploy/test-multi-account.py
	python3 -I deploy/test-session-key.py
	python3 -I deploy/anki/test-native-audio.py

check-web:
	node --check web/app.js
	node --test web/app_test.cjs

lint:
	golangci-lint run ./...

test:
	go test -race ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o $(BIN) ./cmd/anki-review

# The binary for the deploy kit (deploy/README.md). No VCS stamp: deploy/autodeploy.sh builds the
# same flags on the host and restarts only when the bytes differ.
build-linux: build-provisioner-linux
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags='-s -w' -o $(BIN_LINUX) ./cmd/anki-review

build-provisioner-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags='-s -w' -o $(BIN_PROVISIONER_LINUX) ./cmd/anki-review-provisioner

run:
	go run ./cmd/anki-review
