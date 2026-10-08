# Further performance opportunities without changing Kanbi's UX

Research date: 2026-10-08. Production reference: `7dbde75a02f9906efc4530d91b23b9cf86dfdb8d`
on PR #359. This report and the nested experiment module change no production
behavior or dependencies. The existing card expansion, inline terminal mode,
scroll anchors, keyboard bindings, and full editable descriptions are constraints.

## Findings and priority

| Opportunity | Evidence | Recommendation |
| --- | --- | --- |
| Raise the renderer maximum to 120 FPS | The real Kanbi program probe improved p95 input-receipt-to-write from 17.045 to 9.036 ms, with identical output volume | First small production experiment; validate power/CPU and real terminals before adopting |
| Render only visible textarea rows and update wrapping incrementally | Both current and latest Bubbles still build the full rendered document; v1 long-line Update alone remains expensive | Best targeted path for long-body editing; preserve text, cursor, wrapping, and all keys |
| Use a renderer that diffs individual screen cells | Identical captured Kanbi navigation frames emitted 1,184 versus 321 bytes per navigation with v1 versus v2 | Strong output-volume opportunity, particularly over SSH; migration needs full UX checks |
| Synchronize terminal frames where supported | Protocol holds display updates until a complete frame is ready | Improves visual coherence; no demonstrated latency gain here |
| Cache unchanged projections | Master still loads full descriptions and projects all tickets on every reload | Worth prototyping for large Master boards; use correct invalidation, not a time-only cache |

The maximum FPS is a rendering cadence, not a promise of 120 visible frames each
second. It does not speed up keyboard repeat or make expensive updates cheaper.
The current ordinary board navigation already spends roughly 1–2 ms in model
update/render in the same-runner results from PR #359. Waiting for a render tick
can therefore dominate light navigation. Long-body editing has a different
bottleneck and needs different work.

## Measured experiments

All new measurements below are local Linux writer-sink or component measurements,
not physical screen presentation. Host: AMD EPYC 9V74, 9 exposed CPUs, Go 1.26.7.
No live database or user's terminal configuration was used. Shared-host timing
noise exists; do not equate these numbers with the earlier 4-CPU CI runner.

### Real Kanbi program, 60 versus 120 FPS

Used the existing `TestPerformanceProgram/simulated-300ms-observation` at the
reference commit, and an isolated worktree with only `tea.WithFPS(120)` added
to the test's `tea.NewProgram` call. Both exercise real input decoding, model
update, and the v1 inline renderer while an asynchronous observation is delayed.

| Rendering maximum | Samples | p50 | p95 | p99 | Bytes/trial | Writes/trial |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 60 FPS | 50 | 16.013 ms | 17.045 ms | 17.072 ms | 4,467 | 11.0 |
| 120 FPS | 50 | 8.012 ms | 9.036 ms | 9.068 ms | 4,467 | 11.0 |

These are a sequential isolated pair. An earlier three-run series also showed
the direction consistently: p95 ranges 17.840–18.499 ms versus 8.844–9.005 ms.
That series overlapped other research work, so the isolated pair is the primary
comparison. Unchanged frames plus the test's idle interval emitted no extra
redraws in both configurations.

This is a roughly 47% p95 reduction to the writer for this fixture. It does not
measure battery impact, sustained repeat, SSH, actual tmux drawing, or monitor
latency. The simulated observer is not a real harness workload. Increasing the
ticker frequency can increase wakeups; those costs remain an adoption gate.

Reproduce the reference case:

```sh
KANBI_PERFORMANCE=1 go test ./internal/tui \
  -run '^TestPerformanceProgram/simulated-300ms-observation$' -v -count=3
```

For the comparison, create a worktree at the same reference, add
`tea.WithFPS(120)` only to that test's program options, and run the identical
command. Do not change the production CLI to run this experiment.

### Identical Kanbi frames, renderer only

The nested [experiment module](../experiments/rendering/README.md) feeds two
actual captured Kanbi views into pinned Bubble Tea v1.3.10 and v2.0.10. Dimensions
are 160x40 and both use inline mode. Each run alternates 100 times, varying a
small delay between sends to sample different phases of the renderer ticker.

| Renderer | Maximum | p50 Send-to-write | p95 | Bytes/navigation | Writes/navigation |
| --- | ---: | ---: | ---: | ---: | ---: |
| v1.3.10 | 60 FPS | 10.351 ms | 15.312 ms | 1,184 | 1.00 |
| v1.3.10 | 120 FPS | 5.227 ms | 7.330 ms | 1,184 | 1.00 |
| v2.0.10 | 60 FPS | 10.624 ms | 15.620 ms | 321 | 1.00 |
| v2.0.10 | 120 FPS | 5.581 ms | 7.682 ms | 321 | 1.00 |

The v2 renderer reduced output by 72.9% for these frames, without a latency win
at equal cadence. This is not evidence of a general 73% reduction for every Kanbi
screen. Startup, shutdown, and capability queries are excluded from the measured
interval; no terminal capability responses are supplied, so synchronized output
is not activated. Both renderers passed the no-extra-idle-writes check. The probe
does not interpret the escape stream to verify final terminal cells, which must
be checked with real terminals before migration.

### Textarea component, v1 versus latest v2

Pinned Bubbles v1.0.0 and v2.2.1, width 108, height 12, no line numbers, unlimited
characters, virtual cursor in both versions. Exactly 1 KiB or 64 KiB of ASCII
text initially, with a 32-line variant adding 31 newline bytes. Three warmup
keystrokes and 100 timed keystrokes; complete final values must equal the original
body plus all 103 typed characters. Component styles/dependency versions are not
a complete reproduction of Kanbi's modal, so this is not migration parity.

| Initial body | Layout | v1 Update+View p50 / p95 | v2 Update+View p50 / p95 |
| --- | --- | ---: | ---: |
| 1 KiB | Single logical line | 0.665 / 1.022 ms | 1.056 / 1.488 ms |
| 1 KiB | 32 logical lines | 0.627 / 0.890 ms | 1.457 / 1.894 ms |
| 64 KiB | Single logical line | 25.699 / 30.920 ms | 39.076 / 43.756 ms |
| 64 KiB | 32 logical lines | 9.077 / 11.026 ms | 21.715 / 24.460 ms |

The latest textarea is slower in this component probe. A renderer migration
cannot be assumed to improve editing. Current v1 already memoizes wrapping per
logical line and width. Its `View` still renders every wrapped row before the
viewport clips it; v2 does this in `view()`, including from both `Update` and
`View`. This was verified in the pinned downloaded source, not inferred only
from the open upstream issue.

Separate v1 measurements diagnose two costs:

| 64 KiB layout | Update-only p50 / p95 | Warm View-only p50 / p95 |
| --- | ---: | ---: |
| Single logical line | 18.226 / 20.588 ms | 6.835 / 7.605 ms |
| 32 logical lines | 1.850 / 2.541 ms | 6.892 / 8.065 ms |

Do not add separately sampled percentiles. Visible-row rendering should address
the document-wide display work. A long logical line also invalidates its wrap
cache after each insertion: incremental rewrapping from the changed segment,
with stable suffix reuse where possible, is a second necessary experiment.
Do not insert newlines, truncate bodies, cap paste, or alter soft wrapping to
achieve the improvement. A focused upstream patch or carefully maintained
component patch is preferable to a new editor with different key semantics.

## Projection caching: useful, but invalidation comes first

The final PR #359 measurement still puts a 2,000-ticket Master reload at roughly
42 ms median and 31 MB allocated. Named-board refresh no longer loads unrelated
tickets, but Master intentionally includes them. `MasterBoardViewWithFilter`
also runs one projection query per workflow column. Batching those queries is a
separate straightforward prototype; profile it rather than assuming query count
is the dominant remaining cost.

Potential cache design: retain immutable ticket content, column/board metadata,
and stable projection fields; overlay freshly read narrow runtime/workspace/note
state when needed. Key filtered results by the entire effective filter and
board identity. Bound retention, copy mutable slices/checkpoint pointers, and
preserve full-body search, exact inspector/edit values, ordering, and archived
semantics. Caching changes periodic reload and large Master behavior; ordinary
navigation already uses the in-memory board snapshot.

A naive `PRAGMA data_version` cache has three pitfalls:

1. Values can only be compared over time on the same physical SQLite connection.
   A pool limited to one connection is not a guarantee that it will never replace
   that connection; replacement must invalidate the saved comparison.
2. Commits on that same connection do not advance its version. Kanbi's separate
   file-backed reader can observe writer commits, but `OpenMemory` uses one
   connection and needs an explicit mutation generation or a cache bypass.
3. Active session observations intentionally update heartbeat timestamps on
   every poll. Those commits invalidate a whole-database cache even when visible
   card content is unchanged. Do not suppress lifecycle heartbeats to get hits.

A temporary two-connection WAL experiment confirmed reader version 2→3 after
a writer commit while the writer stayed at 2; a reopened reader returned 2.
A single in-memory connection stayed at version 1 after its own commit. These
numbers illustrate the connection-local behavior, not portable global revision
identifiers. Avoid falsely claiming hits by testing only an inactive database.

Cross-process CLI changes must remain visible. If a revision is checked around
a projection read, the cache must not stamp an older snapshot with a newer
revision during a concurrent commit. Use a consistent transaction/snapshot and
careful invalidation, retry when needed, and test connection replacement.

## Terminal rendering and visual stability

Kanbi currently uses Bubble Tea's default inline v1 renderer. It skips identical
frames and unchanged lines; it does not redraw the entire screen for every
keystroke. The v2 Cursed Renderer uses a screen-cell model and finer updates.
Its synchronized-output path brackets a completed frame with terminal mode
2026 only after support is determined. A terminal can then display the complete
frame together instead of showing partially erased/rebuilt rows. This improves
coherence; it does not fix wrapping, database work, keyboard repeat, or guarantee
lower physical latency.

Keep inline mode. Alternate-screen mode changes shell scrollback and exit
restoration behavior, so it is not a free performance change under the same-UX
constraint. A blanket v2 upgrade also requires adapting `tea.View`, key/paste
messages, component APIs, and companion Bubbles/Lip Gloss libraries. Clipboard,
cursor, Unicode widths, images, resize, suspend/resume, terminal cleanup, and
fallback behavior all need regression checks. The textarea result above argues
for measuring each component before selecting the migration approach.

tmux's current manual documents synchronized-update capabilities. Actual
benefit depends on the installed tmux version and outer terminal; do not force
features the terminal does not support or overwrite the correct pane `TERM`.
Its `escape-time` setting handles ambiguity after an Escape byte. Plain `h/j/k/l`
navigation does not have that ambiguity, so changing it is not a demonstrated
fix for board navigation. Physical rendering through the user's emulator and
SSH connection remains unmeasured in these probes.

## Adoption gates

For 120 FPS: repeat the real input-to-write tests on an unloaded runner, test
continuous repeat/typing, CPU at idle and under load, and slow output/SSH. Use
the Kanbi UI validation skill at 160x40 and 80x24 for board navigation, selection
expansion, scroll anchoring, modals, and resizing. Compare actual captured frames
and terminal cleanup, not only sink timing.

For textarea work: compare visible output and final text/cursor positions against
the existing component across Unicode/graphemes, tabs, blank lines, long words,
paste, wrap-boundary edits, deletion, movement, resize, scroll, cancel, and save.
Keep 64 KiB single-line and multiline cases, plus normal-size bodies.

For projection work: test external writes, heartbeats, latest inactive sessions,
workspace changes, note counts, filters, archive/delete, focus/checkpoints,
cache mutation isolation, concurrent readers/writers, and in-memory stores.

Then run the repository's full fmt/test/vet/smoke and relevant race/focused checks
for any production adoption. This research change itself has no production UI
change. Its reproducible probes passed normal tests, race tests, and vet; the
final renderer probe also received a race check after packaging.

## Primary sources

- [Bubble Tea v1.3.10 API: WithFPS](https://pkg.go.dev/github.com/charmbracelet/bubbletea@v1.3.10#WithFPS)
- [Charm v2 announcement](https://charm.land/blog/v2/): renderer, inline mode, synchronized output. Upstream general speed claims are not Kanbi measurements.
- [Bubble Tea migration guide](https://github.com/charmbracelet/bubbletea/blob/main/UPGRADE_GUIDE_V2.md)
- [Bubbles migration guide](https://github.com/charmbracelet/bubbles/blob/main/UPGRADE_GUIDE_V2.md)
- [Pinned v2 renderer source](https://github.com/charmbracelet/bubbletea/blob/v2.0.10/cursed_renderer.go)
- [Pinned v1 textarea](https://github.com/charmbracelet/bubbles/blob/v1.0.0/textarea/textarea.go) and [v2 textarea](https://github.com/charmbracelet/bubbles/blob/v2.2.1/textarea/textarea.go), inspected via Go module downloads.
- [Open upstream textarea issue #1023](https://github.com/charmbracelet/bubbles/issues/1023), consistent with source inspection.
- [Ghostty synchronized-output documentation](https://ghostty.org/docs/help/synchronized-output)
- [tmux manual](https://man.openbsd.org/tmux): `terminal-features`, `Sync`, and `escape-time`.
- [SQLite data_version documentation](https://www.sqlite.org/pragma.html#pragma_data_version)
- [Final production performance run](https://github.com/carlotran4/kanbi/actions/runs/37806417479/job/113411759599)

New probe source, pins, captured fixtures, and raw selected output are under
`experiments/rendering`. Results are research evidence, not approval to migrate
or a claim that all listed opportunities have been implemented.
