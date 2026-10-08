# Performance issue #358: implementation and evidence

The changes remove periodic runtime observation and SQLite lock waits from keyboard processing, reduce hidden-board work, and bound repeated preview/Markdown rendering. They retain Bubble Tea v1's production inline mode and default 60 FPS renderer. This addresses the blocking and rendering portions of [#358](https://github.com/carlotran4/kanbi/issues/358); the separate renderer migration and terminal experiments remain open.

## Reference comparison

[Successful paired run](https://github.com/carlotran4/kanbi/actions/runs/37794839615/job/113371482889): baseline `d88094d5184fdd9bc607ef0b46c6c5c04f2d0866`, candidate `b5abc83c9a4cda623ce124d0b69cff0652f07010`. Both run sequentially on the same Ubuntu Linux amd64 runner, Go 1.26.8, tmux 3.4, four exposed CPUs, AMD EPYC 7763. These are reference-host measurements, not estimates for a user's laptop. Timing samples include Go garbage collection; allocation columns measure Go heap allocation, excluding SQLite C allocations and subprocess memory.

All timing triplets below are **p50 / p95 / p99 in milliseconds**. Named-board fixtures have 30 visible tickets with 1 KiB bodies. The hidden fixture adds 2,000 other-board tickets with 8 KiB bodies. Body fixtures use ten visible tickets; notes use fifty distinct Markdown notes of approximately 1 KiB each. CPU/render measurements warm up three times before collecting samples.

| Operation | n | Baseline p50 / p95 / p99 | Candidate p50 / p95 / p99 |
| --- | ---: | ---: | ---: |
| Stable View, 80x24 | 200 | 0.651 / 0.884 / 1.427 | 0.554 / 0.793 / 1.344 |
| Navigation Update + View, 80x24 | 200 | 0.845 / 1.589 / 1.710 | 0.599 / 1.287 / 1.452 |
| Stable View, 160x40 | 200 | 1.960 / 2.726 / 2.957 | 1.833 / 2.688 / 2.976 |
| Navigation Update + View, 160x40 | 200 | 2.009 / 2.924 / 3.166 | 1.864 / 2.639 / 2.922 |
| Stable View, hidden fixture, 80x24 | 200 | 0.653 / 0.911 / 1.450 | 0.547 / 0.895 / 1.248 |
| Navigation, hidden fixture, 80x24 | 200 | 0.844 / 1.658 / 1.821 | 0.600 / 1.487 / 1.622 |
| Stable View, hidden fixture, 160x40 | 200 | 1.968 / 2.745 / 3.029 | 1.822 / 2.559 / 2.724 |
| Navigation, hidden fixture, 160x40 | 200 | 1.999 / 2.990 / 3.184 | 1.860 / 2.694 / 3.005 |
| Refresh + named-board projection, no hidden tickets | 100 | 2.075 / 2.391 / 2.436 | 0.654 / 1.198 / 1.368 |
| Refresh + named-board projection, hidden fixture | 100 | 53.191 / 59.576 / 62.423 | 0.605 / 1.024 / 1.260 |
| Master projection, no hidden tickets | 40 | 1.476 / 2.076 / 2.115 | 1.238 / 1.735 / 2.163 |
| Master projection, hidden fixture | 40 | 50.863 / 57.787 / 58.513 | 48.382 / 51.180 / 53.306 |
| Navigation, 1 KiB body | 200 | 0.785 / 1.409 / 1.616 | 0.616 / 0.651 / 0.691 |
| Body typing Update + View, 1 KiB | 100 | 2.690 / 3.702 / 3.818 | 2.049 / 2.992 / 3.185 |
| Navigation, 64 KiB body | 200 | 12.252 / 19.970 / 20.574 | 0.634 / 0.923 / 1.496 |
| Body typing Update + View, 64 KiB | 100 | 41.102 / 43.424 / 43.860 | 33.036 / 34.754 / 35.154 |
| Notes thread, fifty notes | 200 | 28.267 / 29.874 / 35.137 | 0.019 / 0.547 / 0.846 |
| Complete notes modal, fifty notes + 64 KiB body | 120 | 44.392 / 46.991 / 51.670 | 1.451 / 2.934 / 3.357 |
| Ten real Git worktrees, warm refresh | 30 | 154.397 / 160.370 / 162.396 | 0.105 / 0.170 / 0.181 |
| Ten real active tmux sessions, refresh | 30 | 55.860 / 57.739 / 58.499 | 55.922 / 59.826 / 69.308 |
| Input receipt to renderer write, 300 ms observer | 50 | 316.422 / 317.278 / 317.487 | 15.942 / 16.957 / 17.004 |
| Input receipt to renderer write, real 300 ms SQLite writer | 50 | 349.861 / 350.589 / 350.829 | 15.841 / 16.741 / 16.915 |

Cold Git refresh has one sample: 157.028 ms baseline and 156.969 ms candidate. Warm samples fit within the five-second successful-observation cache; this does not make uncached Git commands faster. Every workspace's persisted status is checked after observation. All ten tmux sessions are real private windows, and their stored active state is checked. Their median refresh cost is unchanged and candidate tail timing is higher in this run; no tmux subprocess speedup is claimed. These observations now run away from keyboard processing.

| Operation | Baseline bytes / allocations per operation | Candidate bytes / allocations per operation |
| --- | ---: | ---: |
| Navigation, 80x24 | 255,955 / 887 | 219,861 / 674 |
| Navigation, 160x40 | 443,776 / 2,312 | 416,345 / 2,134 |
| Refresh + named-board projection, hidden fixture | 31,778,520 / 76,647 | 253,033 / 1,368 |
| Navigation, 64 KiB body | 998,145 / 928 | 109,799 / 742 |
| Body typing, 64 KiB | 6,047,061 / 43,910 | 5,447,961 / 43,169 |
| Fifty-note thread | 11,685,864 / 206,085 | 47,387 / 951 |
| Notes modal with long body | 13,741,139 / 215,535 | 406,938 / 2,487 |
| Ten worktrees, warm refresh | 2,675,906 / 13,194 | 32,992 / 430 |

The program test uses a real Bubble Tea program in production inline/60 FPS mode. It waits for the initial selection frame, starts a background observation, injects a navigation key, timestamps input-reader receipt, and watches the output writer for the changed ticket identity. The writer scenario holds an actual SQLite `BEGIN IMMEDIATE` transaction for 300 ms. Output averages **4,467 bytes and 11 writes per trial for both versions**; this change does not claim lower renderer traffic. Five no-op messages plus idle time produce no additional writes, excluding a minute-clock boundary. This test measures program receipt-to-write latency, not physical emulator/GPU presentation or a PTY transport delay. The real tmux UI test below verifies terminal behavior separately.

## Changes and invariants

- One coalesced, bounded asynchronous refresh returns a board snapshot and Focus status. Applying snapshots stays on the model thread; projection generations reject obsolete results. Selection, top-visible ticket identity, editor binding, and unsaved buffers survive refresh. Errors keep the last valid snapshot with a stale warning. Shutdown cancels and joins work before closing storage.
- Focus status uses a read query, with an immediate return when disabled. File-backed storage has a query-only WAL reader; immediate write transactions still enforce actual Focus admission. In-memory storage retains its single connection.
- Runtime polling selects narrow active-ticket fields, batches named-board card reads, and reuses projection SQL. Observation writes compare the exact session version, so a poll cannot overwrite a newer lifecycle transition. Intentional session heartbeat/output semantics are retained; unchanged workspace JSON does not write or advance its timestamp.
- Successful Git observations cache for five seconds; failed observations retry. The bounded observation cursor rotates through workspaces. Explicit resolve/integration operations still revalidate fresh state.
- Bounded content/width caches share card previews and heights, reuse note Markdown renderers, and cache modal backgrounds. Notes render only a viewport-sized window; all fifty notes remain reachable. The notes tab uses compact description context, saving an older note retains its selection, and small inspectors reserve space for global footer/status rows. Ordinary navigation/refresh avoids vertical backfill; resizing still fills available space.
- Terminal capability probes cache positive and negative results per terminal context and bound subprocess time. Plain inspector dismissal skips graphics probes when no Kitty image has been displayed.

The pointer-based cache helpers were an additional measured improvement: value receivers copied the large TUI model for each card. Profiling long-body editing also found that Bubbles textarea wrapping remains the dominant cost; no dependency migration was introduced just to improve that distinct path.

## Verification and real UI evidence

The linked successful run passes:

```bash
go fmt ./...
go test ./...
go test -race ./...
go vet ./...
./scripts/smoke.sh --skip-checks
python3 scripts/performance-ui.py
```

Focused `go test ./internal/harness ./internal/tmux ./internal/storage` also passes locally. Regression tests cover stale refreshes, coalescing, worker cancellation, read/write contention, session compare-and-swap, cache invalidation, viewport anchors, unsaved drafts, note selection, small-terminal footers, long-body cursor visibility, and Kitty cleanup. Smoke runs fake harness lifecycle paths in real tmux; no real harness CLI behavior was changed.

`scripts/performance-ui.py` invokes `.pi/skills/kanbi-ui-validation/scripts/ui-session.sh` and uses only its disposable local-backend, sync-disabled fixture. It adds ten private sleeping runtime windows, not real agent processes. At **160x40 and 80x24** it sends each navigation key individually (`j` nine times, `k` four, `l` three, `h` three) and captures every frame. It exercises Master and named boards, vertical/horizontal overflow, resize, help, filters, board picker, unsaved title drafts across ticks, fifty notes with older-note edit/save and add/delete, a Unicode/image-placeholder 64 KiB description with edit/save, and navigation/editing during an actual held SQLite writer followed by recovery. Captures and database assertions verify selected cards/text, header/footer, terminal bounds, persistence, and unsaved drafts. The job logs retain these actual terminal frames; inspection confirmed selected notes, cursor text, save controls, and global footers remain visible at 80x24. The private session is stopped in `finally`.

This workspace runs Go 1.26.7, tmux 3.4, and SQLite after installing tools into a writable prefix. The host rejects Unix socket creation with `Operation not permitted`, so local tmux-dependent CLI tests cannot pass here. The complete test suite, race checks, smoke, and skill UI flow pass on the linked runner, which permits real tmux. Installation does not remove that host restriction.

## Reproduction and remaining work

The opt-in tests are skipped by ordinary `go test`. On a host with Go, tmux, SQLite, and Git:

```bash
KANBI_PERFORMANCE=1 KANBI_PERFORMANCE_TMUX=1 \
  go test -v ./internal/tui -run '^TestPerformance(Evidence|Program|External)$' -count=1
KANBI_PERFORMANCE=1 KANBI_PERFORMANCE_ENFORCE=1 \
  go test -v ./internal/tui -run '^TestPerformanceProgram$' -count=1
python3 scripts/performance-ui.py
```

The workflow copies the same measurement test files into a detached baseline worktree, then runs baseline and candidate on one host. Candidate program p95 must be below 50 ms in both blocked-observation scenarios. Normal navigation Update + View is below the issue's 10 ms p95 target at both sizes. This is a reference-host threshold, not a universally deterministic timing guarantee.

Large Master projections still materialize all visible tickets (~51 ms p95 here), though periodic projections now run off the event loop. Editing a 64 KiB textarea remains ~35 ms p95 and misses the normal-board budget; Bubbles wrapping and grapheme work need a separate editor improvement. There is no internal-revision/`data_version` projection cache yet. Selected-card preview expansion remains, so removing backfill reduces motion without eliminating all height changes or reducing compact board density.

Bubble Tea v2/synchronized output, alternate-screen comparisons, fixed-height preview prototypes, SSH behavior, real clipboard/image attachment transport, harness attachment/terminal restoration under a renderer migration, and emulator presentation latency have not been validated by this change. They remain separate experiments in #358. Provider sync is disabled in the UI fixture and its latency is not measured. Do not close the whole issue on the strength of these results alone.
