# Dark card canvas

## Goal

Remove the white card rectangle in the dark review UI, including ordinary Anki
templates that explicitly set a white background and black text. Keep passive
HTML rendering, template formatting, media, and the existing security boundary.

Base: `3a3e043`. Branch: `feat/fix-card-dark-theme`.
No scoped rules or project CLAUDE.md exist. Project AGENTS.md applies.

## Acceptance criteria

1. `go test ./internal/repository/cardrender ./internal/repository/ankiconnect ./internal/handler/httpapi`
   passes, including new tests covering dark defaults, legacy saved documents,
   repeated sanitization, and preserved content. New regression tests must fail
   before source changes; existing preservation/security tests are guard tests.
2. `make check-web` passes; iframe isolation remains unchanged.
3. A real headless Chrome probe using actual `Document` output and `web/style.css`
   verifies computed styles at 1137px and 375px: iframe background equals the app
   panel, document root/body backgrounds are transparent, body text is the app
   light text, color-scheme is dark, and standard light Anki CSS cannot win.
   Also verify night selectors work, child colors and code/image/audio survive,
   and a twice-sanitized saved document behaves identically. Save screenshots
   and command evidence in private scratch. No real accounts or cards required.
4. `make check` and `make build` pass.

## Design and shared definitions

- `web/app.js:231-237` creates sandboxed iframe documents for questions and backs.
  It remains unchanged. The parent cannot theme the child through inheritance.
- `internal/repository/cardrender/document.go:23-24` defines the trusted envelope.
  Add `class="nightMode night_mode"` to html and
  `class="card nightMode night_mode"` to body. Both conventions support authored
  night selectors on either root and descendant `.card` nodes.
- Append this trusted style after sanitized content, before `</body></html>`:
  `:root { color-scheme: dark !important; background: transparent !important; } body.card { background: transparent !important; color: oklch(0.96 0.005 140) !important; }`
  Place it in a style element. Root-only overrides deliberately retain nested
  colors, media, fonts, alignment, and layout. Standard light `.card` rules,
  including `.card` important declarations, lose to the trusted body rule.
  Appending after raw content also protects equal-specificity legacy styles.
- Every return path, including oversize fallback, uses the envelope and theme.
  A second Document pass may preserve a previous theme style; duplicate passive
  declarations are harmless. No special sanitizer exemption is needed.
- `web/style.css:305`: replace only `.card-frame` background `white` with
  `var(--panel)`, so the loading canvas and transparent document match the app.
  Leave sizes, scrolling, borders, and all sandbox/CSP settings unchanged.
- Native night styles with higher specificity and `!important` may supply their
  own root text colors. Nested explicit colors retain their authored meaning.

## Gates and baseline

- Full gates: `make check`, `make build`, `git diff --check`.
- Baseline `make check` passed on the base: lint 0 issues, Go race tests,
  provider fixtures, 23 multi-account tests, 11 audio tests, 17 web tests.
- Baseline scoped command and web command pass; logs in private scratch.
- New tests do not yet exist on base; record their RED result before source edits.
- Before each commit run `make check`, save command, output, and exit status in
  private scratch, and associate its log with the commit. Full suites run serially.

## Tasks

### T1: Regression tests

Own only `internal/repository/cardrender/document_test.go`.
Add table-driven `TestDocumentDarkTheme` covering plain HTML, standard light
`.card { color: black; background-color: white; }`, important light `.card`
declarations, legacy full html/body documents, and `strings.Repeat("x",
maxDocumentBytes+1)` fallback. Parse output with existing x/net/html dependency:
verify html classes contain nightMode/night_mode; body classes contain all three
shared class tokens; final body child is trusted style with the exact shared
declarations. Avoid whole-output equality. For non-oversize input check safe
content remains. Run each ordinary case twice through Document to cover saved
documents. Add a preservation case for nested colored content, code, font CSS,
local image/audio and night-selector CSS. Existing CSP/sanitizer tests remain.
Named mutation: remove trusted trailing theme style from Document's end constant;
new dark-theme tests must fail while package compiles. Reviewer runs this only
in an isolated scratch copy using `go test ./internal/repository/cardrender`.
Scoped gates: `gofmt -l internal/repository/cardrender/document_test.go`,
`golangci-lint run ./internal/repository/cardrender/...`,
`go test ./internal/repository/cardrender` (expected RED before T2).
Save RED log and exit before T2 starts. Do not commit the red-only state.

### T2: Document theme

Own only `internal/repository/cardrender/document.go`. Depends on saved T1 RED.
Implement exactly the trusted envelope/style from Shared definitions. Add one
brief why-comment explaining isolation and native white canvas override.
Preserve sanitation, CSP, media rewriting, size limit, and fallback semantics.
Scoped gates: `gofmt -l internal/repository/cardrender/document.go`,
`golangci-lint run ./internal/repository/cardrender/...`,
`go test ./internal/repository/cardrender ./internal/repository/ankiconnect ./internal/handler/httpapi`.
After green full gates commit T1 and T2 together, then independently review both.

### T3: Parent frame canvas

Own only `web/style.css`. Can run alongside T2 after T1 RED.
Replace `.card-frame` background only per Shared definitions. Scoped gate:
`make check-web`; verify changed CSS selector still uses `var(--panel)`.
Commit in the same green implementation wave and independently review T1-T3
together. Actual computed CSS is proven by the final browser probe.

## Seams

| Producer | Consumer | Shared definitions |
| --- | --- | --- |
| T2 document | T1 tests | Root classes, trusted trailing theme, fallback |
| T3 frame canvas | T2 transparent roots | App panel behind transparent document |

## Not doing

No card data edits, grading changes, API writes, live service restart, deployment,
auto-height redesign, global CSS stripping, sandbox changes, new browser dependency,
or changes to explicit colors on nested content.

## Completion

Use implementer agents for T1-T3, independent plan-reviewer on exact resulting
commit, acceptance-validator for full gates and acceptance evidence. Run
skill-dry-run on this plan before implementation. Commit verified work and push
the scoped branch according to the owner's Git instructions; inspect CI if any.
