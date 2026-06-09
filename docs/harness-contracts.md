# Harness Contracts

This document specifies the verified command surface, session ref capture method, and known limitations for each supported harness.

Built-in harness contracts are localized in `internal/harness/contracts.go`: command defaults, prompt mode, exit keys, ref capture function, and this document's anchor are grouped per harness. `internal/config/config.go` loads those defaults while still allowing YAML overrides.

## Pi

**Binary:** `pi`

### Commands

| Action | Command |
| --- | --- |
| Start open-only | `pi` |
| Start with prompt | `pi <prompt>` |
| Resume | `pi --session <session_ref>` |

### Session Ref Capture

Primary capture uses Kanbi's bundled Pi extension. When starting Pi with a prompt, Kanbi materializes `pi-session-ref-extension.ts` under its state directory and launches Pi with `-e <extension>`. The extension reads Pi's `ctx.sessionManager.getSessionId()` during `session_start` and writes it to the `KANBI_SESSION_REF_FILE` JSON handoff path. Kanbi stores that `sessionId` as `harness_session_ref`.

The handoff file uses a **stable per-ticket path** (`<stateDir>/pi-session-refs/ticket-<id>.json`, no timestamp) so that recovery can reconstruct the path from the ticket ID alone. A background goroutine continues polling the handoff file for up to 30 seconds after the session row is written, ensuring the ref is saved even when the Pi `session_start` event fires several seconds after launch.

Fallback capture still scans Pi JSONL session files under `~/.pi/agent/sessions/**/*.jsonl` for sessions where the handoff file is unavailable. Each file begins with a `{"type":"session","id":"<id>","cwd":"<cwd>",...}` header line. Subsequent lines are message entries, one of which will be the first user message with the prompt text.

Fallback matching requires:
- `header.CWD == os.Getwd()` (same working directory)
- `header.Timestamp` is recent (within the capture window)
- A `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"<prompt>"}]}}` entry matches the rendered prompt

**Verified:** start, extension ref capture, fallback ref capture, close, resume path, recovery from handoff file. See `TestOpenTicketWithPiPromptCapturesSessionRefFromBundledExtension`, `TestCapturePiSessionRefFromSessionFile`, and `TestOpenTicketRecoversMissingPiSessionRefFromRefFile`.

### Exit Keys

`C-c`, `exit`, `Enter`

---

## Codex

**Binary:** `codex`

### Commands

| Action | Command |
| --- | --- |
| Start open-only | `codex --no-alt-screen` |
| Start with prompt | `codex --no-alt-screen <prompt>` |
| Resume | `codex resume --no-alt-screen <session_ref>` |

The `--no-alt-screen` flag keeps Codex output in the normal scrollback buffer, which is required for tmux pane capture and prompt detection.

### Session Ref Capture

Codex writes session history to `~/.codex/history.jsonl` as newline-delimited JSON.

Each entry has:
```json
{"session_id": "<id>", "ts": <unix_float>, "text": "<prompt>"}
```

Capture logic in `harness.CaptureSessionRef` scans for an entry where:
- `entry.text == promptText`
- `entry.ts` is recent (within 2s of session start)
- Picks the entry with the highest `ts` when multiple match

**Verified:** start, ref capture, close, resume path. See `TestCaptureCodexSessionRefFromHistory`.

### Exit Keys

`C-c`, `exit`, `Enter`

---

## Copilot

**Binary:** `gh` (GitHub CLI with Copilot extension)

### Commands

| Action | Command |
| --- | --- |
| Start open-only | `gh copilot --` |
| Start with prompt | `gh copilot -- -i <prompt>` |
| Resume | `gh copilot -- --resume=<session_ref>` |

Open-only and prompt-start use different base commands (`start` vs `start_with_prompt` in the contract) because `-i` requires a `<prompt>` argument and must not be included in open-only launches.

### Session Ref Capture

Copilot CLI writes sessions to `~/.copilot/session-store.db` (SQLite). The schema includes:

- `sessions(id, cwd, created_at)` — session UUID, working directory, creation timestamp
- `turns(session_id, turn_index, user_message)` — per-turn messages

Capture logic in `harness.CaptureSessionRef` queries for the most recent session where:
- `sessions.cwd == os.Getwd()` (same working directory)
- `sessions.created_at >= session_start - 2s` (recency filter)
- `turns.user_message == promptText` at `turn_index = 0` (first user turn matches prompt)

The returned `sessions.id` (UUID) is passed to `gh copilot -- --resume=<id>` for resume.

**Verified:** start, ref capture from `~/.copilot/session-store.db`, close, resume path. See `TestCaptureCopilotSessionRefFromSessionStore`.

### Exit Keys

`C-c`, `exit`, `Enter`

---

## Fake / Smoke Harnesses

Used by `scripts/smoke.sh` and unit tests. Located in `scripts/fake-harnesses/`.

| Action | Behavior |
| --- | --- |
| Start | Prints `PROMPT_READY` and optionally echoes the prompt argument |
| Resume | Accepts `--session <ref>` and starts normally |
| Session Ref | Emits `SESSION_REF=<value>` to stdout when a prompt is received |

Fake harnesses let deterministic smoke tests verify the full lifecycle without real harness binaries, auth, or quota.

---

## Adding a New Harness

1. Add a `Contract` entry in `internal/harness/contracts.go` with `start`, `resume`, `exit`, `prompt_mode`, `CaptureRef`, and `DocsAnchor`.
2. If the harness writes a machine-readable session ref log, add a capture function in `internal/harness/harness.go` and wire it from that contract.
3. Add unit tests in `internal/harness/harness_test.go` for command construction and ref capture.
4. Document the contract in this file.
5. Run `go fmt ./... && go test ./... && go vet ./... && ./scripts/smoke.sh`.

Do not add a new harness if its session ref source cannot be verified locally without consuming quota.
