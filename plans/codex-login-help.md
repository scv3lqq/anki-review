# Explain Codex session import in plain steps

Base: `063e6111f3c836570dcd9ca3a7b640ba4dcda4ba`. Branch: `feat/codex-login-help`.
The current page sends users to a setup guide and mentions a temporary CODEX_HOME
without runnable commands. A user ran plain device login and could not find auth.json.

## Goal and acceptance
A1: Setup/registration and Settings share the same English Codex help, explaining
whole auth.json versus a manually copied token, CLI prerequisite, own ChatGPT login,
device-code browser approval, success, exact file location/file-picker steps, saving,
cleanup only after save, and missing-file troubleshooting.
A2: The help provides separate native details for macOS, Linux and Windows. Commands
force a dedicated private home and file storage, do not read/print credentials, do
not touch/revoke the normal login, and restore temporary Windows CODEX_HOME changes.
A3: deploy/README.md matches the novice page commands byte-for-byte after HTML entity
decoding and keeps advanced VPS transfer commands intact. No host/person/secret in git.
A4: Existing web tests and full make check/build/build-linux pass, independent review
and instruction dry-run pass. No real login, API grading, collection writes or server
restart is performed by a documentation test. Fake CLI only for command verification.

## Shared definitions and design
Use the exact commands and sequence in E1-new below. Stage-local commands must use
unique task variables, no global CODEX_HOME export on POSIX, and guarded chaining.
Windows creates and restricts its folder before login and restores previous home in
finally. Show OS commands in separate nested native details, no JS controls or new
runtime dependencies. Use shared ordered steps so cleanup blocks follow upload/save,
never appear in the first login block. File picker input remains the existing one.
Keep setup/provider/registration/login/grading code unchanged. Add concise missing-file
and rejected-session troubleshooting; do not direct users to an existing auth cache.
PowerShell availability is optional for local validation: report static-only Windows
review honestly when absent. macOS fake-login snippets and portable shell syntax are
required; Linux /dev/shm branch can be tested with a substitute scratch mktemp during
offline checks. Never invoke real login. Official auth reference already fetched.

## Tasks
T1 owns only web/index.html and web/style.css, the single shared help component.
Replace only tpl-codex with the novice sequence and commands of E1-new, use English
summary 'How to connect your ChatGPT account'. Use semantic lists/details/pre/code,
no new IDs in cloned content. Add only help-scoped list/pre spacing and wrapping so
375px mobile fits without page overflow. No JS or unrelated help edits. Gate:
make check-web; git diff --check; fake CLI/shell dry-run uses decoded HTML blocks.
No new implementation-mirroring tests for static copy. Do not run full gates.
T2 owns only deploy/README.md. Apply exact E1/E2 frozen blocks with PLAN_EDITS and
check the byte-for-byte result. Keep unrelated deployment instructions unchanged.
Gate: PLAN_EDITS check; git diff --check. Do not run full gates.
Root owns this plan and git/release operations only. Implementers share checkout
and preserve each other's changes. T1 and T2 are independent file sets.

## Seams
T1 page commands / T2 guide commands: Shared definitions exact E1-new commands,
platform choice, private temporary paths, no token extraction, cleanup after save.
Existing JS clones tpl-codex for both forms, so static markup requires no JS edit.

## Gates
make check
make build
make build-linux
git diff --check
PLAN_EDITS check plans/codex-login-help.md deploy/README.md 063e6111f3c836570dcd9ca3a7b640ba4dcda4ba
Independent instruction dry-run before implementation and after final text, fake
Codex executable only, no credentials. Independent reviewer bound to file hashes;
validator runs full gates serially, logs in private task scratch.

## Not doing
No auth protocol/token changes, credential inspection, real device login, card writes,
new copy-button JS, grading changes, broker/container/image changes, unrelated docs.
Deployment uses setup-anki-review with fresh private saved-pass/capacity/backup checks
and preserves every account. If active passes require a user decision, prepare the
verified change first and request that specific maintenance decision.

## Frozen documentation edits

**E1.** (lines 104-108)

````md E1-old
Enable device code authentication in your ChatGPT security settings first. During login,
open the displayed URL in your browser, enter the displayed user code, and approve access.
The owner performs these steps in a private terminal; never paste codes or credentials into chat.
See [Codex authentication](https://developers.openai.com/codex/auth).

````

````md E1-new
### Get the file on your own computer (recommended)

For Codex, upload the whole `auth.json` file; do not extract or paste a token.
A plain `codex login --device-auth` can use the OS credential store, so a file
may not appear. The steps below force a separate file-based login for this service.
Use your own ChatGPT account and keep this terminal open until settings are saved.
They work even when anki-review itself runs on a remote server; no SSH is needed.

1. Install Codex CLI 0.166.0 if needed, then check `codex --version` in Terminal
   (macOS/Linux) or PowerShell (Windows). See the
   [official Codex guide](https://developers.openai.com/codex/quickstart).
2. Enable device code authentication in your ChatGPT security settings. Copy and
   run only the block for your computer. These create a private temporary folder
   before login; they do not reuse your usual Codex session.

macOS, Terminal:

```sh
umask 077
anki_codex_dir=$(mktemp -d "${TMPDIR:-/tmp}/anki-review-codex.XXXXXX") &&
CODEX_HOME="$anki_codex_dir" codex -c 'cli_auth_credentials_store="file"' login --device-auth &&
test -s "$anki_codex_dir/auth.json" &&
printf 'File to upload: %s/auth.json\n' "$anki_codex_dir" &&
open -R "$anki_codex_dir/auth.json"
```

Linux, Terminal (uses memory-backed `/dev/shm`):

```sh
umask 077
anki_codex_dir=$(mktemp -d /dev/shm/anki-review-codex.XXXXXX) &&
CODEX_HOME="$anki_codex_dir" codex -c 'cli_auth_credentials_store="file"' login --device-auth &&
test -s "$anki_codex_dir/auth.json" &&
printf 'File to upload: %s/auth.json\n' "$anki_codex_dir"
```

Windows, PowerShell:

```powershell
$ankiCodexDir = Join-Path $env:LOCALAPPDATA ("anki-review-codex-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $ankiCodexDir -ErrorAction Stop | Out-Null
icacls $ankiCodexDir /inheritance:r /grant:r "$([Security.Principal.WindowsIdentity]::GetCurrent().Name):(OI)(CI)F"
if ($LASTEXITCODE -ne 0) { throw "Could not protect the temporary folder." }
$ankiPreviousCodexHome = $env:CODEX_HOME
$env:CODEX_HOME = $ankiCodexDir
try { codex -c 'cli_auth_credentials_store="file"' login --device-auth } finally { $env:CODEX_HOME = $ankiPreviousCodexHome }
if ($LASTEXITCODE -ne 0 -or !(Test-Path -LiteralPath (Join-Path $ankiCodexDir "auth.json"))) { throw "Login did not create auth.json. Complete browser approval and try again." }
Write-Output ("File to upload: " + (Join-Path $ankiCodexDir "auth.json"))
```

3. While the command waits, open the URL shown in the terminal, sign in to your
   own ChatGPT account, enter the displayed one-time code and approve access.
   Wait until the terminal reports successful login. Never share the code or file.
4. The terminal prints **File to upload** with the exact `auth.json` path. On macOS,
   Finder also selects it. On the anki-review page select **Codex**, choose this
   complete file using the file picker, then submit registration/setup or press
   **Save** in Settings (enter your current password when requested).
   To enter the printed path in a file picker: macOS **Cmd+Shift+G**, Linux
   **Ctrl+L**, Windows the **File name** box. Do not open the file or paste its contents.
5. Wait for a successful registration/save on the page. Only then, in the same
   terminal, delete the temporary folder. Keep it for retry if the page reports an error.

macOS/Linux cleanup, only after successful save:

```sh
rm -rf -- "$anki_codex_dir"
```

Windows cleanup, only after successful save:

```powershell
Remove-Item -LiteralPath $ankiCodexDir -Recurse -Force
```

If no file appears, check that browser approval completed, use the entire command
block including `CODEX_HOME` and `cli_auth_credentials_store="file"`, and look at
**File to upload**, not the current folder or your usual `.codex` directory.
Do not run `codex logout` on your normal session. Imported credentials refresh
inside the service; do not continue using this temporary session elsewhere.
If the saved session is rejected later, repeat this dedicated login and import a new file.
See [Codex authentication](https://developers.openai.com/codex/auth).

### Advanced: create the file directly on the VPS

````

**E2.** (lines 144-148)

````md E2-old
For a local install, use the same temporary `CODEX_HOME` and explicit file-store option with
`codex -c 'cli_auth_credentials_store="file"' login --device-auth`, then select its `auth.json`
on the local setup page and remove that temporary home after successful save. On Linux use a
private directory under `/dev/shm`. On macOS temporary auth files reside on local disk until
removed; rely on the local account permissions and disk encryption for this boundary.
````

````md E2-new
For a local service, use the same personal-computer steps above. Temporary files on
macOS and Windows reside on local disk until deleted; use a private local account
and disk encryption. Linux login uses a private memory-backed folder under `/dev/shm`.
````
