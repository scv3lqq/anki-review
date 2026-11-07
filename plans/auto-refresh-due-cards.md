# Automatically refresh due cards

## Goal
Keep an overnight-open review page current without reloading or disturbing work.
Base: d6537f2. Branch: feat/auto-refresh-due-cards.

## Acceptance criteria
- `make check-web` passes deterministic fake-timer tests: an empty start screen gains
  due decks after 60000 ms; focus, visible visibilitychange, pageshow and online
  trigger a refresh without a reload; hidden and ineligible screens issue no requests.
- The same suite proves selected deck and typed batch count survive; finished result
  rows and grades survive while live More availability changes from 0 to 3 and back
  to 0; delayed responses cannot affect another account/session/screen or a busy UI;
  overlapping events issue one request; errors preserve UI and a later tick retries.
- `go test -race ./internal/usecase/review -run 'TestMore|TestStageOrder|TestWrongSession'`
  proves a written batch with cached More=0 accepts newly due cards, an empty queue
  still rejects More and unconfirmed batches cannot be replaced.
- `make check` and `make build` pass. Automatic refresh uses GET api/decks only.

## Constraints and shared definitions
All implementation and artifacts are English. Preserve unrelated edits and commit
signing. No applicable scoped rules or CLAUDE.md exist. Frontend is plain JavaScript.
No dependencies, backend routes, credentials, deployment or collection writes.
Use the existing account-fenced requestJSON and no-store cache option.
DUE_REFRESH_MS = 60000. Define async refreshDueCards(). One repeating interval is
installed once at script initialization, plus visibilitychange on document and focus,
pageshow, online on window. Hidden or ineligible events are harmless no-ops.
Eligibility: S.authed, !document.hidden, !S.settings, !S.starting, !S.taking,
!S.importing, !S.decksLoading, and start or (results and S.written).
Invalidate S.dueRefresh at every show() call and at the start of startPass, takeMore
and importCards after validation, before mutation requests. showStart calls show(),
so its replacement load invalidates prior refreshes even if busy flags clear before
the old response arrives. Test busy entry and exit followed by stale JSON explicitly.
State adds decksLoading:false and dueRefresh:null, reset with initialState(). Capture
request identity using own token, epoch, screen and session. Recheck all before
applying JSON and only clear own token in finally. Failures leave existing content
and messages untouched; existing login/account-change handling remains authoritative.
Set decksLoading during showStart's initial request, clear only for current account.
Start refresh: update S.decks, preserve S.deck and start-count.value if selected deck
survives; otherwise use saved preferred deck if present, then first deck, then null
and existing pickDeck behavior. renderStart updates max/disabled/counts normally.
Results refresh: boot stores S.deck = data.deck. Set S.more to matching deck.due or 0.
Extract renderResults' availability/footer code into renderResultsAvailability with
an optional preserveCount=false parameter. Background calls preserve existing count
when previous More>0, initializing normal batchSize when availability first appears;
never overwrite focused input. Do not rebuild result rows, mutate grades, messages,
answers, result objects, session, or invoke boot/reload for refresh.
More must require StageWritten, but remove the stale Status.More<=0 precondition;
take already validates actual due items and rejects an empty queue safely.

## Gates
- `make check`
- `make build`
Keep command, exit status and output in private scratch and associate with commits.
Baseline: make check passed on d6537f2, including 17 web tests and all Go/deploy checks;
make build and focused test baseline to run before plan commit. New regression tests
are exempt from baseline pass and must fail before production changes (TDD).

## Tasks
### T1: Regression tests first
Own web/app_test.cjs and internal/usecase/review/service_test.go exclusively.
Extend pageHarness with fake interval registration, window/document event dispatch,
visibility and context access; no real repeating timers. Expose refreshDueCards,
showStart, showResults, startPass, takeMore only as needed through VM test exports.
Keep existing tests passing where production permits. Cases and literal expectations:
empty decks -> Tech due=3 on minute and each resume event; hidden means 0 requests;
selected Tech and typed '2' retained on update to due=5, max='5'; deleted selection
falls back to next due deck; written results more 0 ->3 ->0 updates controls with
identical result row references, grades and answers; focus on count preserves typing;
no requests during input/checking/unwritten results/settings/start/take/import/loading;
one request for concurrent interval/focus; deferred JSON discarded after account,
session, screen or busy change; network error leaves old UI then recovery works.
Backend: cached written More=0 then due items appear -> new input session of 2 cards;
written cached zero plus actual empty -> CodeEmpty; existing stage/session guards pass.
Save completed RED evidence before source writers start. Do not commit red tests.
Scoped gates after implementation: make check-web; go test -race ./internal/usecase/review;
golangci-lint run ./internal/usecase/review/...
Named mutations on isolated copies: remove interval registration -> timer test fails;
remove response identity guard -> stale-response test fails; restore stale More count
precondition -> backend newly-due test fails. Mutation gates: node --check web/app.js
and make check-web; go test ./internal/usecase/review -run TestMore.

### T2: Browser refresh
Own web/app.js exclusively. Depends on T1 completed RED evidence.
Implement shared definitions at initialState, showStart (around line 299), results
footer (around 694), boot deck assignment (around 1254), refresh helper and listeners.
Do not alter grading/status polling or automatic write behavior. Scope gate: make
check-web. Definition of done: all T1 browser tests pass with no real timers in tests.

### T3: Recheck due cards when taking more
Own internal/usecase/review/service.go exclusively. Depends on T1 RED evidence.
At More around line 174, change only the cached count precondition: retain written
stage requirement, existing conflict text and actual take path. Scope gates:
golangci-lint run ./internal/usecase/review/...;
go test -race ./internal/usecase/review. Definition of done: T1 Go tests pass.

## Seams
| Producer | Consumer | Shared definition |
| --- | --- | --- |
| T1 | T2 | refreshDueCards, DUE_REFRESH_MS, eligibility, state and UI contracts |
| T1 | T3 | cached-zero written pass must consult actual queue |
| T2 | T3 | live More availability can become positive after cached zero |
Tests are written first; T2/T3 run on independent files concurrently. Commit the
combined green wave, then independent review of T1-T3 against that commit, followed
by final acceptance validation. Test-first ordering overrides source-first slicing.

## Not doing
Automatic batch start, submitting answers or grades, page reloads, server restart,
live deployment, SSE/WebSockets, dependency changes, or refreshing active passes.
