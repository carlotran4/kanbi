# Rendering research probes

These tests compare pinned Bubbles/Bubble Tea v1 and v2 without changing Kanbi's
production dependencies. This nested module is intentionally outside the main
module's `go test ./...` traversal. Run explicitly with Go 1.26.7 or later:

```sh
go test -C experiments/rendering -v -count=3
go test -C experiments/rendering -race -count=1
go vet -C experiments/rendering ./...
```

See [the research report](../../docs/performance-research.md) for measured results,
limitations, and adoption criteria. Race-instrumented timings are not performance
results. Run performance probes separately from builds and other tests; repeat
on the same machine, and retain individual repetitions rather than pooling tails.

`TestRenderer` feeds identical captured Kanbi frames to both renderers in inline
mode, at 60 and 120 FPS. It measures `Program.Send` to the next output write,
not keyboard decoding, PTY transport, terminal painting, or physical input delay.
Startup/shutdown output is excluded, dimensions are fixed at 160x40, and no
terminal capability replies are supplied. A 50ms idle interval must emit no
additional writes. It does not validate terminal cell contents or styles after
interpreting escape sequences; real terminal validation remains an adoption gate.

`frames.json` contains two consecutive views of an isolated two-ticket test board
at production commit `7dbde75a02f9906efc4530d91b23b9cf86dfdb8d`. It contains no
live-user data. Frames were captured in a temporary test in `internal/tui` with:

```go
m, store, ctx := newTestModel(t)
view := defaultBoardView(t, ctx, store)
createTicket(t, ctx, store, view.Columns[0].ID, "first", "body", "pi")
createTicket(t, ctx, store, view.Columns[0].ID, "second", "body", "pi")
m.reload()
m.width, m.height = 160, 40
frames := []string{m.View()}
m.moveCard(1)
frames = append(frames, m.View())
// json.Marshal(frames), write to experiments/rendering/frames.json.
```

`TestTextarea` uses a 108x12 widget, unlimited characters, no line numbers,
and a virtual cursor in both versions. The ASCII body starts at exactly 1,024
or 65,536 bytes; the 32-line variant adds 31 newline bytes. Three warmup
keystrokes precede 100 measured keystrokes. Tests assert the complete final value
including all 103 inserted characters. Separate v1 Update-only and warm View-only
measurements diagnose costs; their separately sampled percentiles must not be
added together. This is a component comparison, not full Kanbi migration parity.
