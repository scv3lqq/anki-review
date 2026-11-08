# Persistent vault sessions

## Goal
Keep authenticated browser sessions usable until their original 30-day expiry after restart,
including multi-account routing and access to encrypted provider credentials. Preserve review
state and require explicit action before interrupted grading or Anki writes resume.
Base: `2a6e326`.

## Constraints and protection model
- Domain stays independent of infrastructure. Credentials, passwords, raw session tokens and
  recovery keys never enter logs or tracked files. Server session maps and files use SHA-256
  digests only; raw tokens exist transiently at the HTTP boundary and in HttpOnly cookies.
- Existing password encryption remains. An optional, independently generated 32-byte server key
  wraps the current password-derived vault key plus hashed session records with XChaCha20-Poly1305.
  This deliberately changes protection: application data plus the server key permits credential
  recovery without the password. Data-only backups retain password protection; full host backups
  that contain both require equivalent protection. Never silently regenerate a missing key.
- Existing accounts need one successful password login after enabling recovery. Do not read or ask
  for the owner's password. Existing cookies cannot reconstruct previously lost server state.
- Key path is absolute, outside repository and application data; key is a regular non-symlink,
  exactly 32 bytes, mode 0600 or 0640 (no execute, other permissions or group write), parent is
  protected against untrusted replacement. Fail startup for configured absent/unsafe keys;
  unset path preserves legacy password-only operation without persistent sessions.
- No local scoped rule directory exists. Follow AGENTS.md. Preserve Claude defaults, selected
  provider, preferences, OAuth refresh resealing, answers and confirmed grades.
- Live host is multi-account with auto-deploy off. Ordinary restart/install helpers refuse it.
  Use setup skill's explicit multi-account maintenance, private saved-pass inspection and backups.
- Group related files into compilation-unit tasks to keep test-first evidence and cross-file
  contracts reviewable; source and tests have exclusive ownership within each task. This is a
  deliberate grouping of the canonical one-source-file slicing rule, with no parallel shared files.

## Shared definitions
- `domain.AuthSession`: exported `Hash string`, `User string`, `Expires time.Time` with JSON tags
  `hash`, `user`, `expires`. Digests are 64 lowercase hexadecimal characters.
- `config.Config.SessionKeyFile string`, environment `SESSION_KEY_FILE`, empty disables recovery.
  `vault.WithSessionKey(path, accountID string) Option` configures key and stable account binding.
- Optional owner persistence port in usecase: `PersistentVault` embeds Vault and exposes
  `LoadSessions() ([]domain.AuthSession,error)`, `SaveSessions([]domain.AuthSession) error`,
  `Recover() (domain.Secrets,error)`, `SealSessions(user,password string,secrets domain.Secrets,
  sessions []domain.AuthSession) error`, `PersistenceEnabled() bool`.
- `vault.json` remains v1/password compatible, adds optional `recovery` AEAD envelope. Encrypted
  payload holds only current derived key and session metadata. AAD binds version, stable account
  ID, login and canonical current KDF (including salt). Every password reseal atomically replaces
  both password ciphertext and recovery envelope with the selected surviving session hashes.
  Credential Reseal atomically retains recovery records and same derived key. No separate stale
  credentials copy. Missing recovery is a migration state. Invalid configured recovery fails closed.
- `owner.UseCase.Logout(token string) error`; `RevokeHash(hash string) error` supports manager
  eviction without retaining raw tokens; `SessionExpiry(token string) (time.Time,bool)` returns
  original expiry. Authentication state uses hashes even with persistence disabled.
- owner.New loads authenticated session records only, without installing key or unlocking. A live
  token checked by Status/Authorized lazily calls Recover and normal OnUnlock, which application
  wiring makes passive. Invalid, expired or revoked tokens never recover credentials. Failures
  are logged generically and authorization fails; no anonymous unlock. Preserve original expiry.
- New sessions are published only after durable SaveSessions. Logout returns an error on failed
  persistence and HTTP must not claim success. Revocation updates memory after successful write;
  password changes atomically keep only the initiating live session, preserving its expiry.
  Recheck session authorization after external settings checks before committing any update.
- Limit live sessions to 64/account, evict oldest expiry deterministically, durable in owner state.
- Multi-account index stores hashes only. Optional factory interface
  `ResolveSession(token string) (string,bool)` locates account ID by loading authenticated persisted
  records for registered, non-pending accounts (bounded to configured 8 accounts). It must not
  open guest runtimes or call broker for unknown/expired tokens. Manager opens only matched
  account, then owner.Authorize is authority. Cache original expiry from owner.SessionExpiry.
  Existing fake factories remain compatible through type assertion. Do not renew TTL on cache fill.
- `review.Options.PauseOnLoad bool` defaults false for compatibility; application sets true for
  every account. `Service.Resume(session string) error` explicitly resumes checking (when not
  already grading) or confirmed (when not already applying) with existing data. It validates
  session and expiry, never silently changes answers/grades. New readonly `paused` booleans in
  CardsView/ResultsView expose a loaded checking/confirmed pass awaiting Resume. GETs do not resume.
- `POST /api/resume` is an authenticated JSON review route with `{session: string}`; add Resume to
  handler Service interface/fakes. UI presents Resume checking / Resume saving to Anki buttons,
  calls only on click, then existing polling. Existing open-tab polling must detect paused results
  and stop with the same explicit action; do not leave an endless spinner after deployment.

## Acceptance criteria and baseline
1. `go test ./internal/repository/vault ./internal/usecase/owner -run 'Persistent|Recovery'`:
   restart retains original expiry; exact expiry rejects; logout durable; password rotation rejects
   other sessions and old password while initiating session survives; stale envelope rejected;
   missing/unsafe/wrong key fails closed; migration login creates recovery; no plaintext secrets or
   raw session tokens on disk; refresh usable after recovery. New test selection is empty on base.
2. `go test ./internal/app ./internal/usecase/accounts -run 'Persistent|Recovery'`: HTTP cookie
   survives reconstruction for owner and guest, isolation enforced, unknown tokens do not open
   runtimes; selected provider, preferences, answers/results retained. New selection empty on base.
3. `go test ./internal/usecase/review ./internal/handler/httpapi -run 'Paused|Resume'` and
   `node --test web/app_test.cjs`: loading/restoring/requesting status starts zero grading/writes,
   explicit Resume starts correct action once, handles expired/incorrect sessions. Existing Resume
   tests on base exercise legacy auto-resume, new guard tests remain passing.
4. `make check`, `make build`, `make build-linux` pass. Baseline make check is archived privately.
5. Code review and final acceptance bind to exact commits/file hashes. All pre-commit full check
   logs and RED outputs live outside repository in private evidence directory.
6. Commit, push, PR merge, CI test/secrets success; local main fast-forwards to origin/main.
7. Live maintenance verifies binary match, service active, multi-account isolation and key metadata
   without inspecting passwords/credentials, logging in, grading or saving grades. If required sudo
   interaction is unavailable, report deployment precisely as pending rather than claiming success.

## Gates
Full gates (serial): `make check`, `make build`, `make build-linux`.
Before every commit run `make check`, archive output/exit and associate resulting commit.
Scoped Go gates: `gofmt -w <owned Go files>`, `go test -race <owned packages>`,
`golangci-lint run <owned packages>/...` with actual package path expansion.
Frontend: `node --check web/app.js`, `node --test web/app_test.cjs`.
Deploy: `make check-deploy`.

## Tasks
### T1: RED authentication and recovery tests
Own `internal/domain/auth.go` (data contract only),
`internal/repository/vault/recovery_test.go`, `internal/usecase/owner/persistent_test.go`.
Write meaningful table-driven tests for criterion 1 using synthetic secrets and cheap vault KDF.
Use explicit timestamps and expected errors/authorization outcomes. Fail tests before source edits;
archive command output and exit. Compile failures from missing named API are allowed as initial
RED but behavioral tests must assert full lifecycle after implementation. Do not change production
behavior. Source writer waits for saved RED exit. Mutations for reviewer: bypass expiry comparison,
skip durable revocation, accept all recovery AEAD; each must fail relevant focused tests.
Gate: `go test ./internal/repository/vault ./internal/usecase/owner -run 'Persistent|Recovery'`.

### T2: vault and owner persistence
Depends T1 completed RED. Own `internal/repository/vault/vault.go`,
`internal/repository/vault/recovery.go`, `internal/usecase/owner/owner.go`; fixes to T1 owned tests.
Implement shared contracts. Reuse vault.write atomic fsync/rename; bounded regular-file reads.
Protect both recovery metadata integrity and account identity. Key loads once per Vault construction;
never log bytes. With unset key ignore recovery for authorization and retain password access without
silently erasing configured recovery. Avoid package cycles (domain owns session DTO).
Preserve memory cleanup and version concurrency for Codex refresh. Resolve durable-write errors
fail closed, including rename ambiguity, rather than resurrecting revoked sessions.
Scoped gates: race tests and lint for vault, owner. Transitional HTTP interface compile errors are
resolved by T4 before full-wave gates. Test old owner semantics with recovery disabled.

### T3: passive saved passes and explicit resume
Own `internal/usecase/review/service.go`, `internal/usecase/review/views.go`,
`internal/usecase/review/paused_test.go`. Independent of T1/T2.
Write tests first and save behavioral RED output before implementation. Use PauseOnLoad, Resume
and paused fields per shared contracts. A nil/idle/input/results/written pass is never resumable.
Ensure Close cancellation, errors, session TTL and existing auto-resume behavior stay covered.
A passive expired pass must not be graded or written through Resume. Keep saved answer/grade data.
Mutations: remove PauseOnLoad guard, call startWriting on read, omit Resume session check.
Scoped gates: `go test -race ./internal/usecase/review`,
`golangci-lint run ./internal/usecase/review/...`.

### T4: wiring, multi-account routing and HTTP
Depends T2 and T3. Own `internal/config/config.go`, new config recovery tests,
`internal/app/app.go`, `internal/app/accounts.go`, new `internal/app/persistent_test.go`,
`internal/usecase/accounts/accounts.go`, new accounts persistence tests,
`internal/handler/httpapi/handler.go`, new resume handler tests and existing HTTP fakes/tests
requiring Logout/Resume signature updates. Existing accounts tests may update raw-index assumptions.
Write integration/routing tests and save RED before edits. Wire configured key per account stable ID;
load via app config and passive review. Preserve registry compatibility; manager never stores bearer
values. Forward durable logout failures through existing error handler. Use factory resolver bounded
registered account IDs, filesystem only until match, generic failures, no credentials in errors.
Gate: race tests and lint for config/app/accounts/httpapi. Mutations: skip manager fallback lookup,
use current-time-plus-TTL on restore, set PauseOnLoad false in app.

### T5: browser paused state
Depends T3 API shape. Own `web/app.js`, `web/index.html`, `web/app_test.cjs`.
Test first existing synthetic browser harness, save RED. Display clear English action; preserve
account epoch handling. No automatic Resume calls. Live polling handles paused response after a
restart; checking/results/saving state must not spin forever. Do not store credentials or session
bearers in browser storage. Gate frontend commands. Mutation: automatically post resume on boot
must fail action-count assertions.

### T6: provisioning and operational documentation
Depends shared contracts, can run after T4/T5 source waves. Own `deploy/install.sh`,
`deploy/install-multi-account.sh`, `deploy/test-multi-account.py`, `deploy/test-provider.sh`,
`deploy/systemd/anki-review.service` if necessary, `.env.example`, `.gitignore`, `README.md`,
`deploy/README.md`, `AGENTS.md`, `.agents/skills/setup-anki-review/SKILL.md`,
`.agents/skills/setup-anki-review/references/server.md`, `.agents/skills/setup-anki-review/lib.sh`.
Do not edit independent Claude skill sources. Installer generates key on host only using secure
randomness, protected create-if-absent publication, root owner/service group 0640 in 0750 directory;
never overwrite/rotate existing key. Persist only SESSION_KEY_FILE path in environment. Update
multi-account installer without unlocking or starting grading. Add synthetic offline fixture checks
before changing scripts; preserve existing maintenance refuses and backups. Document enabling,
one-time login, lost key (fail startup, recover from protected backup or explicit migration with
password login and sessions revoked), deliberate key rotation/logout semantics, backup model and
explicit Resume. Update stale always-locked/automatic-resume guidance for configured recovery;
legacy mode remains password login. Use canonical plan_edits.py for existing prose ranges, capture
old/new in separate plan edit artifacts and verify exact application. Gate deploy fixtures, docs
splice checks and full gates by orchestrator. Skill dry run reviews plan before this task and final
workflow after changes.

## Seams
| Producer | Consumer | Shared definitions |
|---|---|---|
| T1 | T2,T4 | AuthSession and PersistentVault contracts |
| T2 | T4 | hashed lifecycle, Logout/RevokeHash/SessionExpiry, vault option |
| T3 | T4,T5 | PauseOnLoad, Resume, paused view fields and route |
| T4 | T6 | SESSION_KEY_FILE wiring and stable account ID |
| T6 | live maintenance | key provisioning, passive restoration and migration |

## Not doing
No owner/password credential retrieval; no live grading, Anki writes, collection movement or sync
configuration changes; no new provider defaults; no transparent key regeneration; no session TTL
extension; no unrelated Claude instruction changes; no automatic key rotation.

## Design clarifications before source
- T3: Resume on nonresumable stage returns CodeSent, `this batch has no interrupted operation`.
  Checking with existing GradeError is not paused; Regrade remains its explicit retry action.
  Paused checking Results returns success with Session, Paused=true and empty Items, no answer keys;
  browser handles paused before showResults. Confirmed returns normal results plus Paused=true.
  Resume calls expireLocked before session validation, keeps existing passTTL boundary, and after
  Close rejects CodeBusy `review service is closed`.
- T6 ownership also includes `.agents/skills/setup-anki-review/server.sh` for key path forwarding
  and recovery-aware status/wait guidance. Keep independent Claude sources unchanged.
- Baseline evidence: make check passed (49 web tests plus Go/race/lint/deploy); make build passed.
- T6 dry-run corrections: add `.agents/skills/setup-anki-review/check.sh`, new
  `deploy/session-key.py` and synthetic `deploy/test-session-key.py` to ownership. Share one
  provisioning helper across installers and include it in server.sh kit_top/kit_all. Autodeploy
  already compares all tracked deploy files and needs no special manifest change.
- Key helper: ensure_key(directory,uid,gid,allow_create) checks protected ancestors, uses private
  temporary file plus no-overwrite link publication, validates regular non-symlink, 32-byte size,
  owner/group/mode and races. Never chmod/chown an unsafe existing key. Installers allow first
  creation only when no SESSION_KEY_FILE was previously configured. Configured missing/unsafe key
  refuses before installer mutations. Fixtures run with temporary directories/current IDs and
  cover missing configured key, unsafe parents/symlinks, no overwrite, publication race and no
  secret output. `make check-deploy` must run the new offline fixture.
- T6 may edit Makefile only to add that fixture command. Diagnostics inspect key metadata without
  displaying bytes. Update stale automatic-resume text in check.sh and all owned setup files.
- T6 compatibility correction: the independent legacy `.claude/skills/setup-anki-review/server.sh`
  also transports the deploy kit. Permit only its kit_top/kit_all manifest additions for the shared
  key helper; do not rewrite its instructions or migrate its configuration. Its existing fixture
  upload test must pass. This narrow transport dependency overrides the earlier no-edit statement.
- T5: saving polling reads api/results (status plus paused) instead of api/status so open tabs detect
  a paused write after restart. It does not rebuild rows or grades on every tick. This accepts one
  bounded results payload per existing polling interval without adding domain status fields.
- T2 fail-closed clarification: if a vault contains recovery but SESSION_KEY_FILE is unset, refuse
  persistence/account construction rather than permit legacy-mode revocations to resurrect when
  recovery is re-enabled. Pre-recovery vaults remain usable in legacy mode. Canonicalize trusted
  parent aliases (macOS /tmp and /var) before containment/protected-ancestor checks; key itself may
  never be a symlink. Document disabling via explicit password-authorized recovery migration.
- T4 ownership includes existing `internal/app/multi_account_test.go`: replace automatic post-login
  grading/write expectations with passive expectations followed by explicit /api/resume actions.
  Factory keeps registry Store reference and reloads registered nonpending records for bounded
  lookup; reject empty or over-256-byte bearer candidates before disk work.
- T4 existing accounts_test harness/fakeFactory owner.Options.Now uses manager's shared fake clock;
  retain original expiry assertions. This adapts the old independently computed expiry seam.
- Reviewer mutation gates (isolated copies, build first): T1/T2 `go vet ./internal/repository/vault
  ./internal/usecase/owner` then `go test ./internal/repository/vault ./internal/usecase/owner`;
  T3 `go vet ./internal/usecase/review` then `go test ./internal/usecase/review -run 'Paused|Resume'`;
  T4 `go vet ./internal/config ./internal/app ./internal/usecase/accounts ./internal/handler/httpapi`
  then `go test ./internal/config ./internal/app ./internal/usecase/accounts ./internal/handler/httpapi`;
  T5 `node --check web/app.js` then `node --test web/app_test.cjs`.
- T6 server-path correction: do not forward local SESSION_KEY_FILE through S install (a local path
  is not valid on the host). Remote installers preserve saved server path or choose their protected
  default only at first enable; local lib.sh loads the local path for diagnostics only.

## Iteration 1: review corrections
### T4-F1: align cache eviction and prove original expiry
Own `internal/usecase/accounts/accounts.go`, `persistent_test.go`, original T4 implementer.
Independent reviewer reproduced reverse-order restoration of 64 sessions causing manager's cache
sequence eviction to additionally revoke the newest session after owner evicted oldest expiry.
Change manager eviction to ascending original expiry, then ascending hash, identical to owner
sessionRecords order. Remove unused sequence fields/counter. Keep bounded cache/durable RevokeHash.
Add the reverse-restoration regression first, archive RED, and assert exact cache expiry after
fallback in the existing persistence test. The now-plus-TTL mutation must fail that assertion.
Gates: `go test -race ./internal/usecase/accounts`, `golangci-lint run ./internal/usecase/accounts/...`.
Fresh independent review must bind new hashes; previous source verdict cannot be reused.

### T2-F1: always publish a live fresh session
Own `internal/usecase/owner/owner.go`, `persistent_test.go`, original T2 implementer.
Reviewer verified same-clock/smallest-incoming-hash and backwards-clock cases: at capacity the
new session was inserted before eviction and could evict itself while Login returned success.
Evict the oldest existing live record before inserting incoming, preserving original expiry/hash
ordering for existing candidates. Always return a durable live fresh session, even after a clock
rollback. Add both deterministic regressions first and archive RED. Gates:
`go test -race ./internal/usecase/owner`, `golangci-lint run ./internal/usecase/owner/...`.

## Release validation order
Implementation acceptance covers criteria 1-5 and all review fixes, with independent full Gates.
Criteria 6-7 are subsequent release completion checks: execute commit/push/merge/CI and authorized
live maintenance after implementation PASS, then pass their command evidence to the same validator
for final completion audit. They must not be marked MET before those actions actually complete.
This avoids making a safe pre-release verdict depend on an already completed deployment.

## Operational maintenance contract
The private, independently reviewed maintenance script intentionally handles zero guest
reservations only. It checks zero reservations/managed containers/guest registry again after
stopping HTTP and broker, preserves all app state and the existing owner collection, creates and
verifies a streamed encrypted backup with an independent protected random backup key, then
installs both binaries and enables the protected session key. Any guest state aborts before
backup/install and requires the full filesystem maintenance procedure. Preserve the old kit
before upload. Rollback restores old kit, installed and staged binaries and saved settings, then
verifies both services. It retains saved data and never restores collections automatically.
Only anonymous session GET is used, no login/Resume/grading/write probe. Source fingerprints
prove unchanged app JSON, not independently absence of external calls. Sudo requires a visible
terminal interaction; operator never sees password bytes.

### T6-F1: remove stale operational protection/login prose
Own existing deployment README and server reference plus their canonical splice artifacts.
Replace remaining password-only backup protection and unconditional repeat-login claims with the
actual key+data model and cookie restoration. Explain explicit offline recovery metadata removal
preserving password ciphertext; never delete vault as a missing-key repair. Fresh splice checks
and workflow review bind updated hashes.

### T6-F2: preserve isolated binary-layout fixture
Own `internal/provisioner/deployment_test.go` minimal extraction boundary adaptation.
Full `make check` exposed that the fixture extracts binary selection through `for tool in`, now
also including unrelated new session-key preflight. End extraction at the explicit preflight
marker and verify it exists, retaining all expected source/kit/missing binary assertions.
Gates: `go test -race ./internal/provisioner -run TestInstallerBinaryLayouts`,
`golangci-lint run ./internal/provisioner/...`. Independent reviewer verifies unchanged assertions.
