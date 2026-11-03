# Reduce headless Anki WebEngine memory pressure

## Goal

Bound the JavaScript old-generation heap in the headless Anki container while
preserving AnkiConnect, VNC, sync behavior, and its 2 GiB ceiling.
This is a mitigation for the observed WebEngine memory pressure. It does not
claim to identify or repair an upstream memory leak.

Base: `57ba51e70326fd5f81d1128a88ee4ac8efb4b172`.

## Evidence and decisions

- V8 supports a bounded old-generation heap with `--max-old-space-size`:
  <https://chromium.googlesource.com/v8/v8/+/refs/heads/main/docs/benchmarks.md>.
  A smaller heap budget encourages earlier garbage collection.
- Isolated GUI navigation reproduced retained renderer memory. The compositor
  and back-forward-cache workarounds did not reduce it and will not ship.
  A 128 MiB old-generation cap reduced the 200-transition peak from roughly
  956 MiB to 489 MiB. These are synthetic measurements, not owner data.
- Qt supports Chromium arguments through `QTWEBENGINE_CHROMIUM_FLAGS`:
  <https://doc.qt.io/qt-6/qtwebengine-debugging.html#using-command-line-arguments>.
- Pinned Anki's software mode adds `--disable-gpu` only when that environment
  variable is absent. An explicit override must include both flags.
- Configure only the desktop service. The sync service needs no GUI flags.
- Use `--js-flags=--max-old-space-size=128`; retain `--disable-gpu`.
  This bounds V8 old-generation memory, not total process memory. No debug
  port, precise-memory-info flag, or sandbox change is added to production.
- Retain the existing software renderer, memory limits, image pins, secrets,
  ports, capabilities, volumes, and sync settings.
- Before deployment, compare both variants in disposable containers using only
  synthetic profiles. If baseline growth is not reproduced, report that limit
  explicitly; do not describe the workaround as a proven leak repair.

## Acceptance criteria

1. Anki's resolved Compose environment contains exactly
   `QTWEBENGINE_CHROMIUM_FLAGS=--disable-gpu --js-flags=--max-old-space-size=128`.
   The same query on the base configuration is expected to fail.
2. The sync service has no `QTWEBENGINE_CHROMIUM_FLAGS` override; both services'
   memory and swap limits remain unchanged from the base. Prove with resolved
   Compose JSON and a scoped Git diff.
3. Disposable baseline and candidate runs use identical synthetic workloads.
   The candidate completes at least 600 GUI transitions with peak below 768 MiB,
   exit zero, no OOM, and no renderer crashes. Record exact flags: the stress
   probe may add `--enable-precise-memory-info` only for measurement.
   Separately test the exact production flags through at least 150 synthetic
   `answerCards` answers: all succeed, revlog and sum(reps) both equal 150,
   and `deckNames` and VNC remain available. No user volumes, credentials,
   network, or host ports are attached. Synthetic evidence does not prove
   the absence of every possible long-term upstream memory issue.
4. After authorized deployment, AnkiConnect answers `deckNames`, the existing
   deck count is preserved, and the app remains available. Check saved-pass
   stage before any restart and follow the setup skill if a pass is active.
5. All full Gates pass, and task-owned experimental containers are removed.

## Gates

```bash
make check
git diff 57ba51e70326 --check
```

Archive gate output and exit codes under private Git scratch. Local base
`make check` passed before the configuration edit. Live checks use the setup
skill's SSH helper, never raw credential-bearing probes or review API writes.

## Shared definitions

- Desktop service: `services.anki` in `deploy/anki/compose.yaml`.
- New environment value: `--disable-gpu --js-flags=--max-old-space-size=128`.
- Sync service: `services.sync`, unchanged.
- Source scope: only `deploy/anki/compose.yaml`.
- Plan scope: only this file, owned by the orchestrator.

## T1: Configure the desktop JavaScript heap budget

Owner: implementer. Dependencies: isolated experiment and plan dry run complete.
Files: `deploy/anki/compose.yaml` only.

Insert an `environment` mapping under the `anki` service, next to its image:
`QTWEBENGINE_CHROMIUM_FLAGS: "--disable-gpu --js-flags=--max-old-space-size=128"`.
Add one short comment explaining that the headless desktop uses software
rendering and a bounded JavaScript heap to leave memory for Anki and Qt. Use the Shared definitions
literal. Change no other service property, image, or file.

Task gates: `make check-deploy`, `git diff --check`, and a diff confirming only
this mapping/comment were added. Do not run the full Gates or commit.
Definition of done: the exact environment is present only in the desktop
service and every task gate passes. This is a reversible configuration-only
change; use actual Compose and disposable runtime checks rather than a new
implementation-mirroring unit test.

## Seams

| Producer | Consumer | Shared definition |
| --- | --- | --- |
| T1 Compose environment | Existing Anki startup | Software rendering and V8 heap flags together |
| T1 desktop service | Existing sync service | Sync environment unchanged |

## Not doing

- No changes to review, grading, scheduling, collection, or credential code.
- No suppression of Anki's post-sync refresh or changes to its addon sources.
- No failed compositor/BFCache flags, cache/lifecycle tuning, vendor patches,
  or automatic process-restart timer.
- No writes to the owner collection/API, real review sessions, or grader quota
  probes. Synthetic disposable fixtures may write only their own test cards.
- No claim that a short synthetic run proves long-term absence of memory growth.
