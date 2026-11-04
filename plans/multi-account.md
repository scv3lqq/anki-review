# Multi-account invitations and isolated study

## Goal
Allow an owner to invite a friend who registers, provides their own grader credential,
imports an Anki package, reviews and confirms grades independently. Preserve the
legacy owner vault, pass and collection in place. All page strings remain English.

Base: `e576a83`. Branch: `feat/multi-account`.
Private command evidence: `/tmp/anki-review-multi-account-evidence` (not tracked).

## Research and constraints
- `internal/app/app.go:38-109` holds singleton owner/store/grader/review instances.
  Reuse one existing account runtime per account; never swap global Anki profiles.
- `owner.Login` lazily invokes OnUnlock; `review.New` resumes saved grading and
  confirmed writes. Never construct a review service before its own account unlock.
- Legacy `data/vault.json`, `data/state.json`, configured Anki URL and volume stay
  unchanged. Read only the public username in the vault envelope for indexing.
- Claude currently inherits host credentials. Multi-account mode must require an
  explicit Claude credential and isolate HOME/config/tmp/environment per invocation.
  Existing single-account local mode keeps its documented host-login compatibility.
- AnkiConnect pinned source supports importPackage, storeMediaFile,
  getMediaDirPath, deleteMediaFile and retrieveMediaFile. Native import preserves
  models, media and scheduling. Its request cap is 16 MiB: accept at most 8 MiB
  compressed packages through the page. Support legacy-compatible .apkg exports
  (collection.anki2/collection.anki21); reject zstd/nested compressed packages with
  the instruction: "Export with Support older Anki versions enabled."
- Existing card views strip HTML/media. Persist optional rendered HTML alongside
  plain text; only plain text reaches graders. Withhold answer HTML exactly as backs.
- No `.claude/rules` or project CLAUDE.md exists. AGENTS.md applies explicitly.
- Use test-first behavioral changes. Save completed RED evidence before source edits.
- No production server, existing collection or real grader credential is used by tests.
- Deployment work follows `.agents/skills/setup-anki-review/SKILL.md`; instructions
  must preserve saved-pass, backup and sync direction checks. No live rollout in this
  implementation: owner unlock can resume confirmed writes and needs separate care.

## Shared definitions
### Modes and limits
`ACCOUNT_SOCKET` empty keeps legacy single-account operation. A nonempty absolute
Unix socket enables invitations and strict explicit grader credentials for every
account, including owner. `MAX_ACCOUNTS` defaults to 4 (owner included), range 2..8.
No existing owner secrets are changed; an owner previously using host login must
save their own Claude token before grading in multi-account mode.
Opaque guest IDs are 32 lowercase hexadecimal characters from crypto/rand; owner ID
is `owner`. Usernames are trimmed, case-sensitive, 1..64 Unicode runes without
control characters, and globally unique including legacy owner. No user input forms
filesystem paths, Anki endpoint, runtime image, Docker options or keys.

### Account persistence and lifecycle
Add a versioned, atomically replaced, fsynced 0600 account registry below DATA_DIR.
Its public fields are account ID/login/state and hashed invitation metadata, never
passwords or grader credentials. Guest vault/state/preferences are below
`DATA_DIR/accounts/<opaque-id>/`; owner files stay at DATA_DIR. Each runtime has its
own owner.UseCase, vault, filestore, graders and lazy review.Service. Closing the app
closes every initialized review service, then clears all vault handles.
Account creation is serialized and bounded by MAX_ACCOUNTS. Invitations contain
256-bit random bearer secrets, persist SHA-256 only, expire after 24 hours, are
single-use, and max 16 live invitations exist. Owner authentication is required to
create them. Expired invitations may be pruned; used invitations cannot be reused.
Reserve a stable account ID/login against an invitation durably before provisioning.
Publish ready/consume invitation only after vault exists. Failed creation before
vault creation attempts removal of its own runtime and directory. Failed cleanup
keeps a bounded pending record for retry, never allocates another runtime on retry.
A crash with an existing vault must remain recoverable using the chosen login and
password; do not delete that vault. Retry with the same invitation/login completes
or offers login; never reseal an existing vault with a new password. Persistence
errors fail closed; do not report successful registration before durable commit.
Factory.Remove refuses to delete any existing vault using accounts.ErrHasVault.
Before a subsequent Invite/Register, reap expired pending reservations with a bounded
5-second cleanup context outside metadata lock: successful Remove releases slot;
ErrHasVault publishes ready/consumes invite and keeps normal Login recoverable;
unavailable cleanup retains the tracked bounded reservation for a later retry.

### Provisioner contract
A separate root-owned daemon binary `anki-review-provisioner` owns Docker access.
The public HTTP process only uses its group-protected Unix socket and never obtains
Docker socket access. A client in `internal/repository/runtimebroker` exposes
`Ensure(ctx,id) (Endpoint,error)` and `Remove(ctx,id) error`; Endpoint has URL and Key.
HTTP over Unix: POST `/v1/runtimes/<id>` ensures and returns JSON `{url,key}`;
DELETE removes only that managed runtime; invalid IDs fail before Docker calls.
The daemon independently caps guest slots at 3 by default (max 7), serializes
mutations, uses durable private reservations, random secret files (not argv/env),
fixed image `anki-desktop:26.09.3-ac26.10.14.0`, generated container/network/volume names,
loopback-only generated ports starting at 19000, and validates ownership labels on
existing Docker resources. Each container gets its own network and storage, no
published VNC, 2 GiB RAM/swap, 1 CPU, 1024 PIDs, read-only root, bounded tmpfs, all
capabilities dropped and no-new-privileges. New runtime starts an empty profile,
never syncs and requires version==6 readiness before success. Provisioning failures
are retryable under the same reservation; removal never touches owner resources.
Provisioner filesystem roots are administrator configuration, never client fields.
Guest storage is bounded by a documented hard per-runtime filesystem quota or a
fixed-size backing filesystem; do not claim ordinary Docker named volumes have quotas.

### Authentication and HTTP
Keep the existing opaque HttpOnly SameSite=Strict session cookie. Select the account
from a valid session only, never a body/query/account header. Login resolves the
trimmed unique username and calls only its owner use case. Share a bounded global
login throttle and serialize expensive password derivations across accounts.
Unauthenticated session status reveals no other account's unlocked/provider/stage.
Authenticated `/api/session` adds `account_id`, `is_owner`, `managed`, and
`multi_account`. Existing review/settings APIs operate on that account only.
`POST /api/invitations` owner-only JSON returns `{token,expires_at}`; the page builds
`/#invite=<token>` to keep the bearer out of request URLs/access logs.
`POST /api/register` JSON has `invitation` plus existing setup fields; selected grader
credentials are required; infrastructure input is rejected/overridden internally.
Registration sets the ordinary login cookie. Auth errors stay generic. New errors
use English field messages and ordinary 400/401/403/409/429/503 status codes.

### Import and settings
`POST /api/import` accepts raw application/octet-stream bytes only for authenticated
accounts, with required `X-Anki-Import: 1` header (cross-origin preflight barrier),
a same-origin check when Origin is present and no permissive CORS. Reject content
length/stream beyond 8 MiB. Serialize import with account review mutations and allow
it only when stage idle/written; reject active passes with 409 `busy`. Always remove
random staged package files after success/failure using bounded cleanup contexts.
Validate ZIP before Anki: at most 4096 entries, 256 MiB total expanded bytes, no
absolute/traversal/backslash/control-character paths, duplicate paths, encrypted,
symlink or special entries; fully read bounded streams to verify CRC. Legacy
collection must have SQLite header; media JSON maps numeric archive entries to
single safe filenames and all mapped entries must exist. Reject collection.anki21b
or zstd media; never trust declared sizes alone. Stage using random name through
storeMediaFile, obtain server media dir, call native importPackage, cleanup staging.
The imported collection retains its own revlog/model/templates/media; retries merge
through Anki. A failed import does not reset or change the saved review pass.
`GET /api/preferences`, `POST /api/preferences` store account-owned knowledge_decks
(list of <=64 strings <=256 bytes). Owner defaults are existing KNOWLEDGE_DECKS,
guest defaults empty; sync always false for guests. Save only idle/written and apply
to subsequent passes. Settings may never change a managed account's Anki key/URL.

### Adapter symbols (T4 producer, T5 consumer)
`apkg.MaxBytes` is exported 8 MiB limit; `apkg.Validate([]byte) error` validates
legacy-compatible packages. `ankiconnect.Client.ImportPackage(context.Context,[]byte)
error` validates/stages/imports/cleans; `Media(context.Context,string) ([]byte,error)`
retrieves bounded bytes. `cardrender.Document(raw string) string` emits sanitized
standalone document; `cardrender.MediaName(string) bool` validates shared basename
rules. Sanitized Document has CSP meta defense-in-depth; HTTP CSP and sandbox remain
mandatory. Dependency is golang.org/x/net/html v0.62.0 (official docs checked by T4).

### HTML/media contract
Add `QuestionHTML`/`BackHTML` optional fields to domain.Item, persist with pass, and
`question_html` to CardView/ResultItem plus `back_html` to graded ResultItem only.
Existing JSON/plain text behavior remains compatible. cardInfo includes template CSS.
Render GET `/api/render?session=<pass>&n=<n>&side=question|back` from account's own
saved public views, never arbitrary card IDs; back requires a graded, non-skipped
result. Use a script-disabled sandboxed iframe with same-origin media permitted,
strict document CSP (default none, inline styles, local image/font/audio/video only,
no scripts/forms/connections/navigation), no parent DOM access via script. Media
references become authenticated `/api/media/<escaped-basename>`; no filesystem paths.
All Anki JSON responses are bounded at 24 MiB before decoding, detect overflow and
trailing data, and return ErrAnki on violation. A card's combined question/answer HTML
is at most 256 KiB and CSS at most 64 KiB; oversized cards fail with an explicit safe
size error, without silent truncation. These also bound saved-pass amplification.
Media retrieval bounds decoded data (16 MiB), validates basename and returns safe
content types, nosniff and private/no-store; never expose auth snapshots.
Use a real HTML parser for sanitization/rewrite, strip active elements/attributes,
external URLs, forms, base/meta and event handlers; preserve safe formatting/CSS.
Sandbox/CSP remains mandatory even after sanitization. Grader still gets plain text.

### Operations and restart safety
Multi-account anonymous session response includes `multi_account:true` only in
addition to login/setup state. Existing operator scripts must not interpret hidden
unlocked/stage as idle. In multi mode autodeploy holds, server.sh restart refuses,
wait-setup/status explicitly report the mode without waiting for public unlock data.
A maintenance restart stops HTTP first, inspects every account saved state locally
through an administrator-only stage-summary helper, preserves backups and leaves
all accounts locked. Checking/confirmed passes require an explicit owner decision
before any account is asked to unlock. No automatic login or grades during setup. Multi-account provisioning binary updates
are a separate administrator install: build-provisioner-linux, kit upload, run
install-multi-account.sh to stop/replace/restart only the daemon, then HTTP maintenance
restart as needed. Auto-deploy never advances a multi-account deployment, even if
only internal/provisioner changed. Roll back both binaries together and retain
registry/backing filesystems. Both ankiconnect_key and vnc_password secret files
are mandatory for the existing image entrypoint, although VNC is not published.
Provide deploy/check-passes.py with offline fixtures; no private data in its output.

### Page contract
Invited registration reuses setup provider widgets but hides infrastructure and
requires own Claude/Codex credential. Settings hides infrastructure for managed
accounts. Owner settings shows invite creation and link copy. Idle page/settings
has .apkg import, upload errors and older-compatible export instruction. Account
preferences persist server-side. All browser draft/preferences keys include opaque
account ID; clear in-memory review/auth state on logout/account switch and fence
asynchronous replies from prior account. No imported HTML enters parent innerHTML. Main page CSP must permit frame-src self.
Media endpoints never render uploaded HTML as active same-origin documents: unknown,
HTML/JS types are attachment/octet-stream; SVG documents retain sandbox/script-none
CSP. All media responses use nosniff/private no-store and bounded retrieval.

## Acceptance criteria
A1. `go test -race ./internal/app -run MultiAccount -count=1`: two concurrent cookie
jars/collections with identical card ID 1 study and save only their own confirmed
grades; wrong account pass ID gets 409; media/settings/credentials remain isolated.
A2. `go test -race ./internal/usecase/accounts ./internal/repository/accountstore`:
owner-only expiring/single-use invites, duplicate logins, capacity, failed provisioning,
cleanup failure/retry, crash with pending vault, durable registration and restart.
A3. `go test -race ./internal/repository/claude ./internal/usecase/owner`:
strict mode rejects missing explicit credentials before executing CLI; isolated
HOME/config/environment never reads host keys; legacy tests stay green.
A4. `go test -race ./internal/repository/runtimebroker ./internal/provisioner`:
strict IDs, bounds, idempotency, resource labels/limits, retry and no owner deletion.
A5. `go test -race ./internal/repository/apkg ./internal/handler/httpapi`:
auth/preflight, zip bombs/traversal/symlinks/CRC/media mapping, import failure cleanup,
active-pass refusal and answer HTML withholding/sandbox/media authorization.
A6. `go test -race ./internal/app -run MultiAccountRestart -count=1`: restart locks
all accounts and logging into A resumes only A; preserves legacy owner's files.
A7. `node --check web/app.js`; page smoke exercised with fake services in a browser
if available. No live collection or real grader calls. Render/media test fixture
contains formatting, template CSS, image/audio and hostile script/external URL.
A8. `make check`, `make build`, `make build-linux`, plus provisioner Linux build.
A9. Deployment docs explain opt-in migration, owner explicit credential requirement,
no-sync empty guest, package export/import limits, storage/RAM/CPU requirements,
account-aware backups/restore/cleanup, separate privileged daemon and rollback.

## Gates and baseline
Run full gates serially before every commit: `make check`, `make build`,
`make build-linux`; `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath
-buildvcs=false -o bin/anki-review-provisioner-linux-amd64 ./cmd/anki-review-provisioner`
after that command exists; `node --check web/app.js`; `node --test web/app_test.cjs` after T6 exists; `git diff --check`.
Baseline make check PASS, saved as base-check.log. New criterion packages/commands
are absent on base; existing app/owner/http/repository suites pass through make check.
Every task records its focused pre-source RED test command/log then GREEN evidence.
Tests using existing guards are exempt from RED. No overlapping heavy suite runs.
All gate evidence stays in private scratch and is bound to committed tree SHA.

## Tasks
Each task is an exclusive module contract, with file-level source subtasks executed
sequentially by its implementer. Tests land before production changes per AGENTS.md.
This groups related files into bounded transactional tasks rather than separate
commits for every compile-breaking signature edit.

### T1: Explicit isolated grader credentials
Own internal/repository/claude/grader.go, new isolation_test.go,
internal/usecase/owner/owner.go and new strict_test.go; internal/repository/vault/vault.go
and its metadata tests. Add `claude.Grader.Isolated bool`; isolated exec requires token,
uses private temp HOME/work/config/TMPDIR and allowlisted env like Codex, cleans up,
uses bounded output and sanitized generic failure diagnostics. Isolated subprocess
execution is serialized per Grader with context-aware acquisition to bound concurrent
settings checks and grading to one CLI process per account. Add
`owner.Options.RequireCredentials bool`; strict setup/update reject empty selected
Claude token, status connected reflects token, grader still fails closed after legacy
unlock with empty token. Add `vault.User() (string,error)` reads bounded validated
public envelope without decrypting or replacing vault. No legacy fallback change
when strict/isolation flags false. Gates: go test -race ./internal/repository/claude
./internal/usecase/owner ./internal/repository/vault; golangci-lint run those packages.
Mutation: permit empty isolated token or inherit ANTHROPIC_API_KEY, isolation test fails.

### T2: Bounded external provisioner and client
Own internal/provisioner/**, internal/repository/runtimebroker/**,
cmd/anki-review-provisioner/**, deploy/systemd/anki-review-provisioner.*, and
new deploy/install-multi-account.sh. Implement Shared provisioner contract with
injectable Docker command runner and scratch-only fixtures, no live Docker required.
Fixed-size backing filesystem must fail closed if not supported (Linux deploy).
Install separate root binary/service/socket or daemon-bound socket with group mode;
HTTP user remains without Docker membership; daemon roots cannot be writable by HTTP.
Offline fixtures verify arguments, labels, retry, readiness, quotas, cleanup paths.
Do not edit existing installer/Makefile/docs yet. Gates: go test -race
./internal/provisioner ./internal/repository/runtimebroker;
golangci-lint run those packages and ./cmd/anki-review-provisioner;
bash -n deploy/install-multi-account.sh. Mutation: permit slash ID or skip slot cap,
corresponding test fails. Client uses finite deadlines, bounded response, Unix only.

### Account lifecycle API (T3 producer, T5 consumer)
`accounts.Record` has ID, User, InvitationHash strings and Pending bool.
`accounts.Invitation` has Hash string, ExpiresAt time.Time, Used bool, AccountID string.
`accounts.State` has Version int, Accounts []Record, Invitations []Invitation.
`accounts.Runtime` has Auth *owner.UseCase, AnkiKey string,
Close func(context.Context) error. `accounts.Factory` has Open(context.Context,Record)
(*Runtime,error), Remove(context.Context,string) error. `accounts.Store` has
Load() (State,error), Save(State) error. Options has MaxAccounts int, Now func()time.Time,
RootUser string. Account sessions have a manager-owned token index with at most 64
sessions per account, owner.SessionTTL expiry, expired/invalid eviction and underlying
Auth.Logout on eviction outside metadata mutex. Resolve calls Authorized only for the
selected token's account, never scans/blocks on a different account unlocking.
`accounts.New(Store,Factory,*Runtime,Options) (*Manager,error)`.
Manager methods: Resolve(token string) (Record,*Runtime,bool),
Setup(context.Context,client string,in owner.SetupInput) (string,error),
Login(context.Context,client,user,password string) (string,error),
Invite(token string) (InviteResult,error), Register(context.Context,client,invitation
string,in owner.SetupInput) (string,error), Close(context.Context) error.
InviteResult has Token string and ExpiresAt time.Time. Default ID owner is root runtime.
Metadata mutex is never held across Argon2/network/Close; registration has its own
mutex and password operations use a shared semaphore. Resolve snapshots runtime
pointers under lock then checks auth outside it, so A registration does not block B
review requests. Factory may lazily cache account runtimes, bounded by registry cap.

### T3: Account registry and lifecycle use case
Own internal/usecase/accounts/** and internal/repository/accountstore/**.
Follow Shared account lifecycle, using ports for registry/factory; do not depend on
HTTP or infrastructure. Reuse owner.UseCase via a Runtime exposing Auth and managed
Anki key plus Close callback. Factory Open(ctx,Record) returns Runtime; Remove(ctx,id)
cleans only uncommitted guest runtime. Public Manager methods support root setup,
login, resolve session, invite and registration, preferences via runtime adapter,
and closing initialized accounts. Runtime factory is injected; tests fake it.
Use existing domain.ErrCredentials/owner.FieldError where appropriate; exported
sentinels ErrInvitation, ErrForbidden, ErrCapacity, ErrUnavailable, ErrBusy for HTTP.
State types live here so accountstore imports inward. Options include Now, MaxAccounts,
RootUser and explicit root Runtime; pending records never replace owner files.
Use global bounded throttling and password work semaphore. Gates: go test -race
./internal/usecase/accounts ./internal/repository/accountstore;
golangci-lint run both. Mutation: bypass the single-use boundary by removing Used from its primary guard
and changing its secondary AccountID guard to `invite.AccountID != "" && !invite.Used`;
separately skip expiry. Both build and fail TestInvitationsAuthorizationExpiryAndSingleUse.
Removing only Used is equivalent due the independent AccountID protection. Session
isolation tests reject resolving service by caller-supplied ID. No app/http changes in this task.

### T4: Safe package and rendered-card adapters
Own internal/repository/apkg/**, internal/repository/ankiconnect/**,
internal/domain/review.go, internal/usecase/review/views.go and views_test.go,
internal/usecase/review/service.go and focused new collection_test.go,
go.mod/go.sum only if HTML parser needed. Add ankiconnect ImportPackage(ctx,[]byte)
and Media(ctx,name) ([]byte,error); no user path accepted. Provide apkg.Validate([]byte)
with limits above. Add HTML fields and template CSS handling; render sanitizer helper
for HTTP use is in ankiconnect package or dedicated repository/cardrender package
owned here. Add Service.WithCollection(ctx, func() error) error serialized under
service mutex, rejects non-idle/non-written stage, does not reset pass; allow callback
for import/preferences while no concurrent start. Add Service.SetKnowledgeDecks(decks []string) error with its own mutex and same
idle/written guard; add Service.UpdateKnowledgeDecks(ctx context.Context, decks
[]string, save func() error) error for transactional preferences save under the mutex,
then update options only if save succeeds. Decks/take must snapshot knowledge-deck
options under the service mutex to avoid settings races. WithCollection callbacks
must not call Service methods (would double-lock). Tests pin expected safe HTML
properties/plain text and withheld back HTML. Gates: go test -race
./internal/repository/apkg ./internal/repository/ankiconnect ./internal/domain
./internal/usecase/review; golangci-lint run same packages.
Mutations: ignore media mapping traversal or expose ungraded BackHTML; tests fail.

### HTTP adapter API (T5 producer, T6 consumer)
Preserve httpapi.New signature. Add httpapi.NewWithOptions with trailing Options;
Options has AccountID, MultiAccount, Managed, IsOwner metadata and Collection port (AccountID string; the other metadata bool).
Collection interface has Import(context.Context,[]byte) error,
Media(context.Context,string) ([]byte,error), Preferences() ([]string,error),
SavePreferences(context.Context,[]string) error. Existing Service interface stays
unchanged; app collection adapter owns concrete review.Service.WithCollection calls.
NewWithOptions adds import/preferences/render/media routes and session metadata;
legacy New wraps it with zero options. Actual App supplies Collection in all modes.

Add httpapi.Directory interface: Resolve(token string) (http.Handler,bool),
PublicState() string, Setup(context.Context,string,owner.SetupInput) (string,error),
Login(context.Context,string,string,string) (string,error),
Invite(string) (accounts.InviteResult,error),
Register(context.Context,string,string,owner.SetupInput) (string,error).
httpapi.NewMulti(directory Directory, root http.Handler) http.Handler wraps the
existing root handler. Handle global setup/login/register/invitations and anonymous
session directly; authenticated session and all other APIs delegate only to the
resolved account handler. Assets delegate root. Anonymous session returns only
state and multi_account (optionally unlocked:false), never owner progress. Missing
or invalid study session gets 401. Owner invite route requires live session then
owner role; guest gets403. Registration sets ordinary session cookie. Multi wrapper
and child handlers share host/content/preflight guards; no account query routing.

app.New factors account construction internally (one owner/vault/store/graders/lazy
service per account), uses strict/isolated flags for every multi account. Root path,
AnkiURL and sync defaults unchanged. Factory Open obtains broker endpoint/key before
constructing guest (data/accounts/id, syncfalse, empty knowledge defaults) and caches
only that ID. Remove checks on-disk vault.Exists before any broker/storage removal,
returns accounts.ErrHasVault if present; successful service Close removes cached
factory runtime so a future Open builds fresh Auth. Manager controls session lookup
and close; account handler metadata is immutable, never shared global mutable state.
No Ensure at root startup or anonymous requests, so broker outage does not block
owner. NewWithOptions root receives account_id=owner even in legacy mode.

Import guard accepts only exact octet-stream + X-Anki-Import:1, no CORS; if Origin
present reject Sec-Fetch-Site cross-site/same-site. Sec-Fetch-Site same-origin is a
browser-controlled same-origin proof behind a proxy that rewrites Host. Otherwise
compare Origin scheme/host against trusted request scheme/Host (or overwritten
X-Forwarded-Host); malformed Origin rejects. Do not require public Origin to equal
internal loopback proxy Host when browser Fetch Metadata proves same-origin.

Render query requires this account's current pass session, integer N and side.
Question uses CardsView matching session/N. Back uses ResultsView matching session/N
and non-skipped graded item. Wrong session409 session, unknownN404, invalidside400.
Legacy pass without HTML falls back to escaped plain text. Apply Document sanitizer,
text/html, no-store, and CSP sandbox allow-same-origin; default/script/connect none,
styles inline, img/media/font self, base/form none, frame-ancestors self. No scripts.
Media basename validated, lookup only selected endpoint; safe MIME types for images,
audio/video/CSS/fonts, unknown/HTML/JS attachment octetstream and script-none sandbox
CSP on SVG/direct navigation. Guard main CSP permits frame-src self.

For isolated app tests, fakeclaude.Use environment vars are intentionally stripped;
use an explicit test wrapper like fakecodex.Use to inject fixture-only fake vars,
never weaken production env allowlist. New multi broker fixture uses short /tmp Unix
socket paths, loopback ports >=19000 and 64-hex keys (client validates these). Keep
independent token audit fixtures per account without global env races.

### T5: Account HTTP and app integration (depends T1,T2,T3,T4)
Own internal/app/**, internal/handler/httpapi/**, internal/config/**,
internal/testutil/fakeanki/**, new internal/repository/filestore/preferences.go and
preferences_test.go. Preferences adapter exports LoadPreferences(dir string, defaults
[]string) ([]string,error) and SavePreferences(dir string, decks []string) error,
0600 atomic/fsynced preferences.json per-account; missing file copies defaults.
Validate max64 nonempty deck names <=256 bytes, no controls, deduplicate, accept
explicit empty array; missing knowledge_decks is a 400. No user path input.
Preserve existing New API/legacy tests, add opt-in
multi-account server and an HTTP directory adapter in app to keep use cases inward.
Reuse each account's existing handler/service/auth runtime; multi HTTP entrypoint
handles account selection, global login/register/invitations and sanitized session
metadata; static assets reused. Directory maps authenticated token to runtime only.
Expose import/preferences/render/media routes through appropriate account runtime,
strict managed Anki input enforcement. Account URL/key from broker only, no network
call/provisioning while merely starting legacy owner. Guest construction lazy on
login/register; if broker unavailable owner remains usable. Config validates socket
and capacity. Shutdown drains all services. Integration tests include two actual
fake Anki servers/CLIs, concurrent same card IDs, forbidden owner-only route,
invitation reuse and failures, own tokens and Codex refresh, isolated settings/media,
import failure/pass preservation and restart with pending/staged passes. Gates:
go test -race ./internal/app ./internal/handler/httpapi ./internal/config
./internal/testutil/fakeanki ./internal/repository/filestore; golangci-lint run same packages.
Mutations: route B to root service, accept guest key update or resolve render by
foreign pass; corresponding test fails. No real services, credentials or collections.

### T6: Account page (depends frozen HTTP contract, independent of T5 source)
The Shared HTTP/HTML contracts are frozen; T6 can execute in parallel before T5
implementation, without changing backend files.
Own web/app.js, web/index.html, web/style.css and new web/app_test.cjs if
needed. Follow Page contract, use existing provider controls and English page style.
Use iframe pointing at authenticated render route, sandbox allow-same-origin with
no scripts; strict response CSP is server-owned. Parent never inserts imported HTML.
Fence stale async API responses and account-switch drafts. API routes/responses
exactly Shared definitions. Tests first for browser storage namespace and state reset
where meaningful; node --check plus DOM smoke with fake server. Gates:
node --check web/app.js; focused node tests; go test ./web.
Mutation: remove account ID from draft key, focused test fails.

### Stage-summary helper contract (T7)
`python3 deploy/check-passes.py DATA_DIR` runs locally as the data owner/admin, never
contacts HTTP/Anki. Print aggregate stage counts only (idle,input,checking,results,
confirmed,written), never usernames/cards/answers/credentials. Include owner and
all opaque account directories, detect orphan account states as well. Missing state
is idle. Exit0 only when all idle/written, exit2 on any active pass, exit1 on malformed,
oversized (>64 MiB), symlinked or unsafe data. No state changes. Offline fixtures
cover both users, active/confirmed, corrupt JSON, symlink and no secret in output.
Autodeploy fixture must prove multi_account:true holds regardless of unlocked/stage
or unchanged HTTP binary. Both ordinary and sudo restart helper paths must refuse
multi mode; wait-setup returns clear mode status rather than polling. Build/transport
of both binaries is explicit and account status remains private.

### T7: Deployment and documentation (depends frozen contracts, independent files)
The installer/HTTP/page contracts are frozen. T7 may execute alongside T5/T6 once
its exact prose appendix is checked against current T2 flags. Any producer change
that invalidates docs requires a new exact new-fence correction before dispatch.
Own README.md, deploy/README.md, .env.example, Makefile,
deploy/autodeploy.sh, deploy/install.sh, deploy/test-provider.sh,
deploy/check-passes.py and its offline fixtures, .github/workflows/ci.yml for check-web,
.agents/skills/setup-anki-review/** and .claude/skills/setup-anki-review/** only for
required kit transport/runtime instructions. Keep independent skill sources separate.
Use canonical plan_edits helper to snapshot old prose before changing it and apply
exact old/new edits recorded in this plan appendix. Generating that appendix
is a required orchestrator prerequisite immediately before T7 dispatch; T7 must
not begin until it exists. Add build-provisioner-linux and
make check-deploy offline quota/daemon fixture. Transfer kit includes broker binaries,
units and installer; opt-in installation does not start/stop owner or unlock it.
Document rollback: stop guests, disable ACCOUNT_SOCKET, retain files/backups; never
copy guest collection into owner volume. Include migration/backups/resource/import
limits and dedicated credential instructions. Run skill-dry-run on plan before
implementation and changed workflows after. Gates: bash syntax of owned scripts,
make check-deploy; exact plan_edits check for prose. No live deployment.

## Seams
| Producer | Consumer | Shared definitions |
|---|---|---|
| T1 | T5 | Modes and limits, Authentication and HTTP |
| T2 | T5 | Provisioner contract |
| T3 | T5 | Account persistence and lifecycle |
| T4 | T5,T6 | Import and settings, HTML/media contract |
| T5 | T6 | Authentication and HTTP, Page contract |
| T2,T5 | T7 | Provisioner contract, Modes and limits |

## Not doing
Public self-registration, AnkiWeb/self-hosted sync for guests, sharing collections,
remote arbitrary Anki endpoints, arbitrary Docker APIs, card-template JavaScript,
modern zstd packages without an explicit inner-decompression bound, live rollout,
reading credentials, owner collection writes, force pushes or main-branch changes.

## Review and bounded fixes
Independent plan-reviewer reviews committed task contents with hashes and scoped
commands/mutations. Acceptance-validator runs final full gates and all criteria,
receives every review finding and private gate log paths. Max five fix iterations;
stop on repeated finding three times or unavailable required gate, reporting limits.

## T7 exact prose appendix

Before T7 implementation, apply the exact sidecar edits below against base e576a83
with the canonical plan_edits helper, then run the matching check command. Each
sidecar owns only its named file; do not merge E-blocks across files.

- README.md: plans/multi-account-doc-edits/READMEmd.md
- deploy/README.md: plans/multi-account-doc-edits/deploy_READMEmd.md
- .agents/skills/setup-anki-review/SKILL.md: plans/multi-account-doc-edits/agents_skills_setup-anki-review_SKILLmd.md
- .claude/skills/setup-anki-review/SKILL.md: plans/multi-account-doc-edits/claude_skills_setup-anki-review_SKILLmd.md
- .agents/skills/setup-anki-review/references/server.md: plans/multi-account-doc-edits/agents_skills_setup-anki-review_references_servermd.md
- .claude/skills/setup-anki-review/references/server.md: plans/multi-account-doc-edits/claude_skills_setup-anki-review_references_servermd.md

These are independent skill sources, not instruction aliases. Preserve their existing
differences. T7 may add config/script/tool behavior only within its explicit ownership.
The appendix requires final verification against T2 installer flags and T5/T6 user
contracts before dispatch. Correct any factual mismatch in the new fences first.

## Pre-commit independent review corrections

T1 cleanup must handle mode changes made by its own subprocess inside the private
runtime tree without following symlinks. Restore directory permissions before
descending, remove the owned tree, and fail closed with sanitized cleanup error if
removal still fails; discard otherwise successful results. A fake CLI writes a
fixture credential and chmods its private config to 000: test proves no credential
file remains and no silent cleanup failure. Non-timeout fake-process probes use a
10-second budget; the cancellation-wait test keeps its 100ms behavior assertion.

T3 cleanupExpired must propagate registry Save failures (no successful Invite or
Register after failed durable cleanup), retaining consistent retryable metadata.
forgetRemoved must not silently swallow Runtime.Close failure or release its slot
as successful cleanup. Return ErrUnavailable wrapping the failure, keep reservation
tracked for retry and clear Auth only when background close has completed. After acquiring slot.open,
revalidate that the manager still tracks the same slot/record and is not closed
before factory.Open; a stale waiter cannot create an untracked runtime after cleanup. Regression
tests inject Save/Close failures and require a non-successful operation plus bounded
retained reservation; no new invitation may report success after that failure.

T3 Open must re-check the same runtime slot and an existing registry record after
acquiring slot.open, before returning a cached runtime or calling Factory.Open.
Removed or replaced slots and a closed manager fail with ErrUnavailable, never
creating untracked resources. A deterministic blocked-open/expired-cleanup regression
proves this schedule; rerun scoped accounts/accountstore race tests and lint.

T3 post-write durability recovery: add owner.UseCase.RefreshVault() error (metadata
only, setupMu then mu, read Vault.Exists, monotonically set hasVault=true when an
envelope exists, never decrypt/reseal/change secrets). When Factory.Remove returns
ErrHasVault after a failed Setup, refresh cached Auth before publishing or retrying.
Propagate Exists/refresh failure. A persisted-but-error-returning Seal must never be
called again with another password; immediate registration retry authenticates the
original password, and expiry recovery permits normal Login without restart.
T3 exclusive scope extends to owner.go and new refresh_test.go for this fix; T1
writer has finished those files. Tests inject post-write Seal failure and Exists
failure, pin one seal and unchanged original password.

T2 reboot safety correction: managed guests use Docker --restart=no. The daemon
reconciles durable reservations at startup and every 30 seconds, verifying/mounting
the quota filesystem before starting/restarting a container. Service.Reconcile(ctx)
snapshots bounded IDs and calls Ensure sequentially with sanitized aggregated errors;
main runs it in a cancellation-aware background loop while listening on the socket.
A host reboot must never let Docker bind the unmounted fallback directory and start
Anki outside its quota. Fake tests prove mount-before-start and no start on mount
failure; existing container restart policy is verified/migrated safely, never silently
accepted as automatic. No guest runtime can start without verified quota storage.

T7 workflow recheck corrections: updates preserve all four existing daemon settings
explicitly (the installer now retains saved values and explicit overrides win); offline backup preserves complete
per-ID reservation.json/filesystem.json/storage.img/ankiconnect_key/vnc_password after
verified unmount+loop detach; rollback stops only selected labelled guest containers
without DELETE. Exact prose new fences now contain these contracts.

T3 root durability guard: accounts.Manager.Setup also calls metadata-only RefreshVault
before choosing setup, so a post-rename durability failure cannot reseal the owner's
existing vault on retry in multi-account mode. Legacy owner.Setup remains unchanged.

T7 ordinary install guard: deploy/install.sh must detect nonempty ACCOUNT_SOCKET
in explicit settings or existing service env before any install/restart mutation and
fail with the multi-account maintenance instruction. It must not silently restart
a multi-account HTTP service. Add an offline fixture proving the guard precedes
mutation. Multi mode installation is separate, after ordinary owner installation.

T7 build contract: make build-linux depends on build-provisioner-linux so existing
setup references produce the complete two-binary kit without changing their commands.

T5 managed-error privacy: if registration/setup for a managed guest hits owner.FieldError
field anki_key, map it to503 with "Your collection is temporarily unavailable. Try
again." and no infrastructure field/endpoint/key/config names. Runtime/provisioning
errors return generic account-unavailable messages; only opaque account IDs appear
in operation logs. Owner legacy setup/settings keep existing key form compatibility.
