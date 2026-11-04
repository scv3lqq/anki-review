# Show populated imported Default decks

Base: b1049236c94db6c58c32106b0f16d97359b95096.
Branch: fix/imported-default-deck. Actual synthetic import produces one new card in
Default, but GET /api/decks returns an empty list because Client.Decks removes it
unconditionally. The page therefore cannot start that imported collection.

Keep empty Default excluded: Anki can omit it from its deck tree and reject batch
getDeckStats when that empty name is requested. When deckNames includes Default,
issue read-only findCards with query `"deck:Default"`; retain Default when at least
one card exists, exclude only when none exist. Propagate lookup failures with
`fmt.Errorf("ankiconnect default deck: %w", err)`, preserving domain causes.
Use existing response-size bounds. Other names, stats batching, due counts, ordering
and review writes remain unchanged. No new state/settings or writes are required.

T1 exclusive files: internal/repository/ankiconnect/anki.go, anki_test.go and new
internal/repository/ankiconnect/decks_test.go. Write table-driven tests first:
populated Default is listed with correct due counts, empty Default is excluded
before stats (including Default-only empty), no Default makes no extra lookup,
lookup error propagates and no stats/write calls occur. Save completed RED before
source change. Existing TestDecks fake gains only the new read-only lookup returning
empty Default; preserve its prior ordering/stats expectations. New empty/no-default
cases are guard tests exempt from RED. No other files are owned by implementer.

Scoped gates: `go test -race ./internal/repository/ankiconnect`,
`golangci-lint run ./internal/repository/ankiconnect/...`, `git diff --check`.
Independent reviewer checks exact hashes and a mutation that restores unconditional
Default deletion (new populated-Default test must fail).
Full acceptance gates before signed commit: make check, make build, make build-linux,
git diff --check. Reuse unchanged audio/native/infra evidence from prior commit.
Actual deployed acceptance: synthetic Default appears through public API before
study; import/audio/render/fake grade/confirmed save yields own native revlog1.
Owner data, selected credentials, active pass and Anki endpoint remain preserved.
Deployment is an explicit multi-account HTTP maintenance update; no further image,
collection, sync, broker settings, grading or owner unlock changes. No broad cleanup.
