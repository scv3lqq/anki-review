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
before any account is asked to unlock. No automatic login or grades during setup.
Provide deploy/check-passes.py with offline fixtures; no private data in its output.

### Page contract
Invited registration reuses setup provider widgets but hides infrastructure and
requires own Claude/Codex credential. Settings hides infrastructure for managed
accounts. Owner settings shows invite creation and link copy. Idle page/settings
has .apkg import, upload errors and older-compatible export instruction. Account
preferences persist server-side. All browser draft/preferences keys include opaque
account ID; clear in-memory review/auth state on logout/account switch and fence
asynchronous replies from prior account. No imported HTML enters parent innerHTML.

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
after that command exists; `node --check web/app.js`; `git diff --check`.
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
uses bounded output and sanitized generic failure diagnostics. Add
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
RootUser string. `accounts.New(Store,Factory,*Runtime,Options) (*Manager,error)`.
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
golangci-lint run both. Mutation: skip invitation Used/expiry check or resolve service
by caller supplied ID; regression tests fail. No app/http changes in this task.

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

### T5: Account HTTP and app integration (depends T1,T2,T3,T4)
Own internal/app/**, internal/handler/httpapi/**, internal/config/**,
internal/testutil/fakeanki/**. Preserve existing New API/legacy tests, add opt-in
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
./internal/testutil/fakeanki; golangci-lint run same packages.
Mutations: route B to root service, accept guest key update or resolve render by
foreign pass; corresponding test fails. No real services, credentials or collections.

### T6: Account page (depends T5 contracts)
Own web/app.js, web/index.html, web/style.css and new web focused test script if
needed. Follow Page contract, use existing provider controls and English page style.
Use iframe pointing at authenticated render route, sandbox allow-same-origin with
no scripts; strict response CSP is server-owned. Parent never inserts imported HTML.
Fence stale async API responses and account-switch drafts. API routes/responses
exactly Shared definitions. Tests first for browser storage namespace and state reset
where meaningful; node --check plus DOM smoke with fake server. Gates:
node --check web/app.js; focused node tests; go test ./web.
Mutation: remove account ID from draft key, focused test fails.

### T7: Deployment and documentation (depends T2,T5,T6)
Own README.md, deploy/README.md, .env.example, Makefile,
deploy/autodeploy.sh, deploy/install.sh, deploy/test-provider.sh,
deploy/check-passes.py and its offline fixtures,
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
