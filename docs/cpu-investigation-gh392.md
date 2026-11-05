# GH-392 CPU investigation

## Scope and diagnosis

The reported three-day process and its environment were unavailable. Neither the
223–238% CPU observation nor a causal link to GH-358/GH-389 has been reproduced.
Thread count and lifetime CPU averages do not establish a leak or sustained load.

Source inspection identified a reproducible accumulation of custom status command
refresh chains across successful Fleet returns. `initialStatusBarCmd` starts each
referenced custom command. A result schedules its next timer, and the timer starts
the next command. Fleet continues dispatching these messages through the ordinary
status handlers. Returning from Fleet started another chain with the same
`statusBarGeneration`; queued timers and in-flight results from all earlier chains
remained valid. With N successful returns, one configured module could therefore
have N+1 live chains. A default status bar without custom commands is unaffected.

The fix advances the status generation before starting replacement commands after
restoring the board. Old timers and results are rejected by the existing generation
checks. Cached values remain visible until replacement results arrive. Failed
returns retain the current generation. Already executing old commands retain their
configured timeout; their eventual results cannot schedule more work. No agent,
session, history, database schema, or runtime observation semantics change.

## Reproduction and measurement

`TestFleetReturnRetiresStatusRefreshChains` delivers old timer and result messages
across 1,000 successful returns, checking that they cannot restart a chain, while
current results continue refreshing. A separate test covers failed returns.

The opt-in `TestPerformanceStatusBarLifetime` drives a real Bubble Tea loop at
160x40 / 120 FPS with one board, 50 local tickets, zero sessions/notes,
an in-memory SQLite database, and one
`printf` custom command refreshing every 100 ms. It samples fresh and 40-return
states while idle and while navigating at ten keys/second. Fleet completion
messages are injected to isolate scheduling; there are no real agent processes,
provider sync, worktrees, or inspector captures. It reports process CPU (excluding
child commands), command-result counts, allocations, collections, heap sizes, and
goroutine counts. The accelerated interval exposes growth without waiting days;
it is not a three-day soak or a measurement of a production board with agents.

Run matched source revisions with the same measurement test:

```bash
KANBI_PERFORMANCE=1 go test ./internal/tui \
  -run '^TestPerformanceStatusBarLifetime$' -count=1 -v \
  -cpuprofile /tmp/kanbi-cpu.pprof -memprofile /tmp/kanbi-heap.pprof

go tool pprof -top /tmp/kanbi-cpu.pprof
go tool pprof -top -alloc_space /tmp/kanbi-heap.pprof

# Four 15-second sample windows, plus setup/settling:
KANBI_PERFORMANCE=1 KANBI_STATUS_SOAK_SECONDS=15 go test ./internal/tui \
  -run '^TestPerformanceStatusBarLifetime$' -count=1 -v
```

## Measured evidence

On Linux amd64, 12 logical CPUs, Go 1.26.10, production base `570d216`,
matched runs used `CGO_CFLAGS=-O0` for both revisions and three-second windows.
The Go code used normal optimization. These are test-process core percentages
(100% = one logical CPU), not production or terminal-paint measurements:

| Workload | Before, fresh | Before, 40 returns | After, fresh | After, 40 returns |
| --- | ---: | ---: | ---: | ---: |
| Idle | 17.90% | 149.28% | 18.36% | 18.89% |
| Navigation, 10 keys/s | 25.40% | 141.11% | 25.23% | 24.99% |

In the idle 40-return window, command results fell from 296 to 28,
allocated bytes from 312.3 MB to 33.7 MB, and GC cycles from 76 to 9.
Goroutines at the sample end fell from 54 to 11. Navigation results fell from
275 to 28. These counters support elimination of accumulated chains; they do not
establish a general thread or heap leak. The 100 ms custom interval is deliberately
aggressive, so even the fresh baseline is busier than a default idle board.

The before CPU profile attributed 49.86% cumulatively to `Model.View` (44.53%
to `boardView`) and 17.85% to background GC workers. The after profile had
2.61 CPU seconds in `View` versus 10.84 before over the complete runs, including
setup and transitions; total run lengths differ. Allocation profiles place most
allocation in value copies/boxing along the model Update path, followed by
rendering strings. The measurement wrapper itself accounts for about 17% of
allocated space, so allocation totals are not uninstrumented production estimates.
The evidence supports excess messages causing repeated rendering and allocation,
rather than a growing session-history query being dominant in this fixture.

The longer candidate check used normal CGO optimization and four 15-second
windows (64.23 seconds total). Idle fresh/40-return CPU was 19.82%/20.27%;
navigation was 26.43%/28.78%. Each completed with ten goroutines and 124–139
command results per window. This is a short accelerated accumulation check,
not evidence of multi-day stability.

## Verification

- `go fmt ./...`, `go test ./...`, `go vet ./...`: passed. A writable
  `GOCACHE` under `/tmp` was used; the complete suite required local socket
  access for its HTTP test servers.
- `./scripts/smoke.sh --skip-checks`: passed with its isolated Herdr fixture,
  including prompt/ref capture, focus, actual resume and preserved attempts.
- The focused 1,000-return regression failed on the base and passed after the fix.
  Both profiled comparison runs and the 64-second candidate check passed.
- `python3 scripts/fleet-ui.py`: passed on repeat at 160x45 and 80x24, covering
  3–6 agents, direct input, focus/zoom, return, surviving pane geometry and
  SIGKILL/startup recovery with unchanged attempts/terminals. The first candidate
  run failed compact mouse return after resize: its header rectangle was two rows
  instead of four. The unchanged base and repeated candidate passed with a
  four-row rectangle. A temporary driver using keyboard return at that single
  step also passed. The initial resize/mouse failure remains unexplained; no
  mouse or layout code was changed here.
- The project UI-validation helper separately verified Master at 160x45 and
  80x24, then `v` reported no active agents in its fixture. All owned fixtures
  were stopped; production boards and agents were not inspected.

## Remaining incident evidence

This diagnosis is specific to repeated Fleet returns with custom commands. It does
not justify closing the original report as reproduced. For a high-CPU process,
record its version/commit, uptime, current CPU window, board/session counts,
database and history sizes, enabled custom commands, Fleet visit count, inspector
visibility, and workload. Capture CPU, allocation/GC and goroutine evidence during
the high-load window, and compare a fresh process with the same private fixture.
The controlled check above provides test-process profiling without
adding a persistent diagnostic server to Kanbi.
