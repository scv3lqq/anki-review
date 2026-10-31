**E1.** (lines 26-28)

````md E1-old
- `make check`: golangci-lint v2 and `go test -race ./...`; run before each commit.
  Tests use fake AnkiConnect and grading CLI implementations in `internal/testutil`,
  so they need no live Anki or Claude session.
````

````md E1-new
- `make check`: golangci-lint v2, `go test -race ./...`, and `make check-deploy`;
  run before each commit. Deployment checks are shell syntax and offline fixtures.
  Tests use fake AnkiConnect and grading CLIs, so they need no live services.
````
