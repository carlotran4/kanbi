# Performance follow-up: renderer cadence, editing, and projection reuse

This implements the compatible opportunities from [the research](performance-research.md)
without changing inline mode, selected-card expansion, navigation keys, editable
body contents, or runtime heartbeat semantics. The reference before this follow-up
is `001a82f38c1c6b7c5d4020bc1b1d45ee1bc336ae` (production code at `7dbde75`).

## Implementation

- The CLI uses `tea.WithFPS(tui.RenderFPS)`, currently 120. The program performance
  probe uses 120 by default; `KANBI_PERFORMANCE_FPS=60` reproduces the preceding
  production cadence. The existing unchanged-frame output suppression remains.
- A small MIT-licensed copy of Bubbles v1.0.0's textarea retains its original
  editor/cursor/viewport behavior and its upstream regression suite. It avoids
  joining the whole value just to detect emptiness, skips styling offscreen rows
  while retaining viewport row counts, and uses equivalent printable-ASCII width
  calculations. Unicode, controls, dynamic prompts, and line-number gutters retain
  the corresponding original paths. Upstream versus optimized ANSI/state tests
  cover randomized edit/move/delete/paste/resize sequences. ASCII wrapping is
  compared with the original algorithm over 2,000 generated cases. Both release
  packaging paths include the upstream license.
- Board projections have up to eight retained snapshots and a 64 MiB estimated
  snapshot budget. Long filter keys bypass retention. Every access leases the
  same physical SQLite reader throughout its revision check and any projection
  load. Connection replacement clears the cache. File-backed query-only readers
  use `data_version`; single-connection stores additionally check `total_changes`
  and schema revision. The revision is rechecked after loading, so a read crossing
  a commit cannot be retained under a newer revision.
- Admission occurs only after the same key is read again without a database
  revision change. Continuously changing boards therefore retain the ordinary
  SQL read path without cache-copy allocation. Returned slices and checkpoint
  pointers are isolated from retained snapshots. Raw length-prefixed filter keys
  include every field and distinguish invalid UTF-8 without JSON normalization.

The reader lease is acquired before the cache mutex, so a queued projection
retains the connection pool's context cancellation behavior. A regression test
holds another projection open and verifies that a queued read honors its deadline.

Whole-database invalidation is intentionally conservative. A heartbeat, external
CLI change, note edit, workspace change, or lifecycle mutation still invalidates
projections. This cache does not defer freshness with a TTL, and does not claim
hits during continuous writes. Focus membership is annotated by the service on
independent returned projections using the current policy.

Bubble Tea v2/cell rendering and synchronized-output negotiation are not adopted
here. The earlier upgrade probe made textarea editing slower, and a full migration
changes input/component/protocol APIs. The current improvements avoid that extra
migration risk. Physical terminal painting and the user's WSL environment remain
unmeasured.

## Focus Mode horizontal-navigation follow-up

GH-362 reproduced a separate Focus-only path with four columns and 2,000 tickets
per column, split evenly between focused and paused work. Moving horizontally at
card 1,500 preserved the card index but entered a destination column whose viewport
started at zero. The old `vScrollFollow` advanced that viewport one ticket at a
time and recomputed the full focused/paused/archived counts on every attempt. This
made the key update quadratic in the number of tickets; background observation was
already asynchronous and was not the cause.

The follow-up computes each Focus column summary once and finds the earliest valid
destination viewport by walking backward only across rows that can be visible.
Rendering reuses the same summary for section counts and range calculation. Focus
capacity, checkpoint/history data, selected ticket index, viewport anchoring,
editor state, and selected-card preview behavior are unchanged. A randomized
comparison checks the optimized scroll result against the former incremental
algorithm over focused, paused, and archived partitions.

On one Linux amd64 host (Go 1.26.9, 12 CPUs), matched 30-sample `Update` probes for
`h`, `l`, Left, and Right at 80x24 and 160x40 measured these ranges across all key
and size cases:

| Deep horizontal Update | Before p50 / p95 range | After p50 / p95 range |
| --- | --- | --- |
| Focus disabled | 15.060–23.672 / 28.810–72.325 ms | 0.134–0.471 / 0.260–1.054 ms |
| Focus enabled | 171.369–184.404 / 221.932–289.094 ms | 0.235–0.963 / 0.483–1.933 ms |

The separately measured stable `View` was not the reproduced blocker: before the
fix its Focus-enabled medians were 1.140–3.721 ms; after the fix the full run was
1.338–10.927 ms with shared-host/GC outliers on the 8,000-ticket synthetic frame,
and a repeated representative wide `l`/Right subset was 3.918–4.712 ms. The
remedy targets the synchronous navigation update rather than claiming a renderer
improvement.

A real Bubble Tea inline/120 FPS probe kept a refresh observation blocked while
each key was read. Across ten trials per key/size case, Focus-enabled receipt-to-
writer latency changed from 216.387–315.818 ms p50 and 332.066–491.684 ms p95 to
8.055–32.281 ms p50 and 8.299–33.768 ms p95. The matching Focus-disabled after
range was 7.995–25.015 ms p50 and 9.000–40.437 ms p95. These numbers include
renderer scheduling and a deliberately large frame, but not physical terminal
paint.

## Local paired evidence

Same Linux amd64 host, AMD EPYC 9V74, nine exposed CPUs, Go 1.26.7. New measurement
sources are copied into the reference checkout; baseline program probes use
60 FPS and the candidate uses 120 FPS. These are model/service or output-writer
measurements, not physical input-to-paint latency.

| Operation | Samples per run | Reference p50 / p95 | Candidate p50 / p95 |
| --- | ---: | ---: | ---: |
| 64 KiB body typing, full Kanbi Update + View | 100 | 29.085 / 37.506 ms | 6.553 / 10.600 ms |
| Warm Master, 2,000 additional tickets, memory DB | 40 | 39.013 / 45.287 ms | 0.435 / 1.416 ms |
| Master immediately after a mutation, memory DB | 20 | 40.824 / 46.409 ms | 39.570 / 51.479 ms |
| Program input receipt to output, 300 ms observer | 50 | 16.084 / 17.257 ms | 7.981 / 8.439 ms |
| Program input receipt to output, real 300 ms SQLite writer | 50 | 16.074 / 17.110 ms | 7.939 / 8.777 ms |

The continuously invalidated Master case has no demonstrated speedup; its tail
varies. Its Go allocation stays essentially flat: 31,127,442 → 31,129,512 bytes
per operation. Warm Master allocation drops from 31,124,444 to 2,183,128 bytes.
64 KiB body typing drops from 5,445,340 to 4,263,660 bytes and 43,161 to 27,128
allocations per edit. Program probes still emit 4,467 bytes and 11 writes per
trial, with no extra unchanged/idle writes.

Additional repeated checks address misleading tails or cache-only fixtures:

- A real file-backed 2,000-ticket Master fixture runs warm reads, committed
  writes from a second Store, and heartbeat-only writes from that Store. Across
  three runs, median warm p50 is 51.321 → 0.274 ms; warm allocation is about
  28.10 → 2.17 MB. External-write p50 ranges overlap (49.848–55.447 versus
  51.170–57.948 ms), as do heartbeat p50 ranges (51.919–55.327 versus
  51.655–54.446 ms). These misses do not gain from the cache and do not allocate
  retained copies. The first isolated candidate run was slower on misses;
  repeats show shared-host variation rather than a consistently faster miss.
- Three isolated normal-body runs show body-typing p50 1.720–1.792 ms versus
  1.469–1.525 ms. Navigation p95 remains about 0.94–1.00 ms. Title-typing tails
  vary; that code path is not optimized by this follow-up.
- A static actual Kanbi frame measures renderer-only idle CPU at 60 and 120 FPS.
  Three 1.5-second samples use roughly 0.94–1.22% versus 1.12–1.53% of one core
  on this shared host, and emit zero idle bytes/writes. Raising cadence increases
  wakeups; this is a small measured CPU cost, not a zero-cost or battery claim.

The Performance evidence workflow also compares the PR's original base and the
pre-follow-up commit, when that commit remains available in fetched history,
on the same runner. That runner supplies actual tmux and Git fixtures.

## Verification and reproduction

Local deterministic `go test ./internal/storage ./internal/tui/...` passes.
Focused `go test -race ./internal/storage ./internal/tui/... ./internal/harness
./internal/tmux` and `go vet ./...` pass. The native release helper was executed
and its archive checked for the exact textarea license. Full local `go test
./...` hits only the two existing tmux CLI integration failures caused by this
host denying Unix sockets; local smoke reaches the same environment restriction.
The required complete test/race/vet/smoke and real skill-driven UI flow run on the
GitHub runner. Their final results and frame review are recorded in PR #359.

The UI driver now verifies the entire Unicode, image-reference, and
long-description body plus the typed suffix, including Kanbi's existing automatic
`image 1` alt-text numbering, rather than only checking for the suffix. A model
regression test also checks the complete saved value. It still uses only the project's isolated,
local-backend, sync-disabled fixture and stops it in `finally`. The flow covers
Master/named boards, individual navigation keys, overflow, resize, notes,
unsaved drafts across refreshes, and SQLite writer contention at 160x40 and 80x24.

```sh
go fmt ./...
go test ./...
go test -race ./...
go vet ./...
./scripts/smoke.sh --skip-checks
python3 scripts/performance-ui.py
KANBI_PERFORMANCE=1 KANBI_PERFORMANCE_TMUX=1 go test -v ./internal/tui \
  -run '^TestPerformance(Evidence|Program|External|FileProjection)$' -count=1
KANBI_PERFORMANCE=1 go test -v ./internal/tui \
  -run '^TestPerformanceRendererIdle$' -count=3
```

For a paired comparison, copy `internal/tui/performance*_test.go` to a worktree
at the reference commit and set `KANBI_PERFORMANCE_FPS=60` there. Run the same
cases serially on the same machine. Keep CPU/renderer idle measurements separate
from other tests. Do not use race-instrumented timings as performance evidence.
