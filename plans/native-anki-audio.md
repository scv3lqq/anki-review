# Native Anki audio rendering

## Goal and acceptance
Fix native `[anki:play:q:N]` / `[anki:play:a:N]` output so imported sound/video
files play through the existing authenticated media endpoint. Preserve owner data,
account isolation, native template/filter ordering and question/back separation.
Base: `756ffdf67c0315634045fb2e3890da9a64a8a875`; branch `fix/native-anki-audio`.
Private evidence: `/tmp/anki-review-multi-account-evidence/audio-*`.

A1: Python tests prove one native render output supplies both HTML and ordered AV
lists; question cannot reveal answer-list media, and answer may reuse FrontSide.
A2: Python tests cover conditional/reordered outputs, TTS occupying an index,
unknown/out-of-range/forged markers, text-only replacement, URL/HTML escaping,
portable basename rejection and video elements. No note-field reconstruction.
A3: patch tests prove pinned-source hash and unique anchors before writes, original
four-key API shape, and refusal without modifying mismatched source.
A4: deployment fixtures prove both setup helpers transport new image build inputs;
image/install/provisioner pins agree. A Go guard proves existing sanitizer turns
native audio into authenticated passive markup and keeps back audio off question.
A5: `make check`, `make build`, `make build-linux`, strict Python/ruff and independent
review/acceptance pass. CI passes before merge. Actual Linux new-image native
conditional/TTS/front/back tests and deployed synthetic import/audio/grade/save pass.
A6: maintenance retains owner volume, endpoint, preferences, credentials and pass;
backup/rollback covers matching binaries, kit and image. Only synthetic writes.

## Evidence and design
Pinned Anki Card.render_output() owns question/answer HTML and native per-side AV
lists after filters and conditionals. Use one output, never fetch another Card for
metadata after rendering. Primary source:
https://raw.githubusercontent.com/ankitects/anki/26.09.3/pylib/anki/cards.py
https://raw.githubusercontent.com/ankitects/anki/26.09.3/pylib/anki/template.py
Pinned AnkiConnect Services.cards_render currently drops these lists:
https://raw.githubusercontent.com/FooSoft/anki-connect/760ff48d8bbcfbc956f57c40475a8ab960c36a9c/plugin/services.py
Original services.py SHA256:
`3c1132b842f11c30f3fc20f49ea9ba49c67921d8934aac43fd49c1e058d27fd7`.

## Shared definitions
New image: `anki-desktop:26.09.3-ac26.10.14.0-audio1`. Anki/AnkiConnect versions,
UID/GID, resource limits, profile seeding, sync and secrets remain unchanged.
Owned native_audio.render_card(card) returns the original cardId/questionHtml/
answerHtml/css object, preserving native cardId as str(card.id). Call card.render_output() once, question_and_style() and
answer_and_style() on that output; css comes from card.note_type()['css'].
Use HTMLParser with convert_charrefs=False, preserving markup/entities; replace
markers only in ordinary text nodes, never attributes or script/style raw text.
Question resolves q:N only through question_av_tags. Answer resolves q:N from that
list and a:N from answer_av_tags. Preserve every index, including TTS/unknown tags.
Unsupported/malformed/out-of-range references become passive `Audio unavailable`
English text. Never infer filenames from note fields or search another AV index.
Accept only native tag filename strings that satisfy Go MediaName semantics:
nonempty, not dot/dotdot, valid UTF-8 <=255 bytes, no slash/backslash/colon/control.
URL-encode the basename and HTML-escape attributes. Emit controls/preload=none;
video suffixes mp4/webm/ogv/m4v/mov/mkv/avi use video, other files use audio.
Keep Go sanitizer/CSP and authenticated rewriting unchanged. Bound original and
rewritten combined HTML to256KiB, CSS64KiB, AV lists4096 entries; fail closed
with ValueError('Card rendering exceeds size limits') for all three bounds.

Patch script applies only the pinned file hash and exactly one import/function
anchor. Add `from .native_audio import render_card` after the protocol import;
replace only Services.cards_render body with `return render_card(self.card(id))`.
Pure patch_source(source, expected_sha256=PINNED_SERVICES_SHA256) supports fixture
hashes in unit tests; CLI always uses pinned hash, validates regular source, and
copies the owned native_audio module before installing patched source. An already
patched exact result is accepted idempotently; other content is refused unchanged.
Original body anchor:
````python
    def cards_render(self, id):
        card = self.card(id)
        return {'cardId': id, 'questionHtml': card.question(), 'answerHtml': card.answer(), 'css': card.note_type()['css']}
````

## Tasks and ownership
T1 native adapter and patcher: exclusively deploy/anki/native_audio.py,
deploy/anki/patch_ankiconnect.py, deploy/anki/test-native-audio.py. Write meaningful
unit/security tests first; save completed RED before implementation. Standardlib,
Python3.11+ with Protocols/dataclasses and strict types; no Anki imports for tests.
CLI: `python3 patch_ankiconnect.py PLUGIN_DIR`; module source beside patch script.
Patch guards and all A1-A3 cases above are required. Gates: test script, ruff,
strict mypy of these three files, py_compile. Do not edit Dockerfile/Makefile.

T2 image and installation contracts: exclusively deploy/anki/Dockerfile,
deploy/anki/compose.yaml, internal/provisioner/service.go,
deploy/install-multi-account.sh, Makefile, deploy/test-multi-account.py,
.agents/skills/setup-anki-review/server.sh and install-ankiconnect.sh,
.claude/skills/setup-anki-review/server.sh and install-ankiconnect.sh.
Test transport expectations first with both new Python inputs and save RED; then
update both kit manifests, three image pins, Dockerfile patch invocation through
bundled Python, local installers before dest publication, and check-deploy invoking
new unit script. Fresh local install remains atomic and refuses existing addon;
patch failure never publishes dest. Check python3 >=3.11 before any download or
publication and fail with an English prerequisite error for missing/old Python.
Add offline fixtures for these prerequisite failures and adapter failure before
publication. No Claude/Codex or owner runtime settings change.
Gates: deploy fixtures, bash syntax all touched shell files, scoped provisioner
race+lint, git diff --check. Coordinate T1 completion before Python-dependent gates.

T3 docs and Go guard: exclusively deploy/README.md,
.agents/skills/setup-anki-review/references/server.md,
.claude/skills/setup-anki-review/references/server.md and
internal/repository/ankiconnect/collection_test.go. Add native <audio> HTML guard
that passes before/after (behavior preservation); no Go production edits. Document
pinned audio adapter, English unavailable handling for unsupported TTS, and image
upgrade: stop HTTP/broker after saved-pass checks+backups, stop/remove only verified
managed guest containers (retain volume/network/backing/key/reservation), update
image+broker and Ensure to recreate preserving storage. Refresh owner image with
same named volume/secrets/network/sync settings, not a new collection; no owner pass
reset or automatic unlock. Existing saved HTML stays intact until normal next pass.
Do not restart sync merely for an audio-only desktop image update. Both server
references must run `S status`/saved-pass checks and detect multi-account mode
before `S kit-diff`/upload/`S up`. Multi-account mode uses explicit maintenance.
For audio-only refresh show desktop-only compose up with --no-deps and the exact
warning: "Do not run `S up` for an audio-only update." Preserve ordinary full setup
semantics; do not change up.sh itself. Gates: scoped
ankiconnect race+lint, diff check and exact prose checks.

## Seams
T1 produces patch_ankiconnect.py and native_audio.py; T2 includes/transports them
and invokes patcher before AnkiConnect publication. T3 documents this exact API.
T2 owns image pin; provisioner, installer and compose must agree on shared new tag.
T1 native HTML flows through existing Go rendering; T3 guards that seam unchanged.

## Gates and rollout
Full gates run serially after scoped tasks: make check, make build, make build-linux,
ruff+mypy of new Python files, git diff --check. Independent skill-dry-run reviews
plan/helper workflow before implementation; independent reviewers bind hashes;
acceptance-validator owns final gates. Preserve signed commits; make a fix PR and
merge only after CI and actual native image tests. Deployment follows setup skill,
explicit account-aware maintenance/backups and image-aware rollback. Never invoke
ordinary single-owner install or reset the owner's pass to refresh cached HTML.
No personal deployment details, tokens or collection data enter tracked artifacts.


## Exact task gates and review mutations
T1 runs:
- `python3 -I deploy/anki/test-native-audio.py`
- `uvx ruff check deploy/anki/native_audio.py deploy/anki/patch_ankiconnect.py deploy/anki/test-native-audio.py`
- `uvx mypy --strict deploy/anki/native_audio.py deploy/anki/patch_ankiconnect.py deploy/anki/test-native-audio.py`
- `python3 -m py_compile deploy/anki/native_audio.py deploy/anki/patch_ankiconnect.py deploy/anki/test-native-audio.py`
T2 runs:
- `python3 -I deploy/test-multi-account.py`
- `go test -race ./internal/provisioner`
- `golangci-lint run ./internal/provisioner/...`
- `bash -n` separately on deploy/install-multi-account.sh and both setup server.sh/install-ankiconnect.sh copies.
T3 runs:
- `go test -race ./internal/repository/ankiconnect`
- `golangci-lint run ./internal/repository/ankiconnect/...`
- `git diff --check`
- Python exact prose gate: README contains `Audio unavailable`, `--no-deps` and
  `Do not reset`; both server references' server-update section has literal
  `Do not run `S up` for an audio-only update.` and its first `S status` precedes
  its first `S kit-diff`.
All tasks run `git diff --check -- <their explicit owned files>`. Full gates include
T1 ruff/mypy commands above plus make check/build/build-linux and diff check.
New Python behavior tests and new transport/prerequisite tests must fail on base;
the Go native-audio sanitizer guard is explicitly behavior-preserving and must pass
on base. Save finished RED logs before behavioral edits. Baseline full gates from
accepted PR3 tree remain valid because this branch base has identical tree.

Dockerfile must copy both native_audio.py and patch_ankiconnect.py beside the
existing scripts in /usr/local/lib/anki before invoking bundled Python with
`-I /usr/local/lib/anki/patch_ankiconnect.py /opt/ankiconnect`. CLI copies the owned
module into the plugin only after source/version/anchor guards succeed.

Named isolated review mutations (never in shared checkout): M1 permit a:N on
question, M2 filter unsupported AV tags before indexing, M3 replace markers in
attributes/style, M4 bypass source hash guard, M5 omit native_audio.py from one
transport manifest. Each must be caught by its focused tests. Source/test hashes
bind verdicts; no repeated full suites solely for mutation evidence.

## Not doing
No grader/provider change, collection sync, new media service, raw note-field audio
reconstruction, TTS synthesis, transcoding, owner pass rewrite/reset, broad Docker
cleanup, credentials inspection, or automatic unlock. Preserve all unrelated edits.
