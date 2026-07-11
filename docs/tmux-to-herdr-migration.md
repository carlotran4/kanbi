# tmux to Herdr Migration Semantics

Kanbi treats the configured multiplexer as the runtime for **new** terminal containers. Existing session rows keep their recorded owner (`tmux` or `herdr`) until a user explicitly starts or resumes a new attempt.

This is intentional: a stored tmux window id/session name must not be rewritten to Herdr unless Kanbi has created a real Herdr pane/agent to point at. Backfilling rows to say `multiplexer=herdr` without a Herdr container id would make the ticket appear attachable to a process that does not exist.

## Recommended semantics

When `multiplexer.default` changes from `tmux` to `herdr`:

1. **Never rewrite a live tmux session in place.** If the latest active session is a valid tmux window, opening the ticket should keep/focus tmux. The user may choose to keep working there, close it, or start a fresh Herdr attempt.
2. **Resume stale or inactive tmux sessions into Herdr only with a valid harness ref.** If the latest tmux container is missing or inactive and `harness_session_ref` is present, opening/resuming can create a new Herdr pane/agent using the harness resume command. The new active session row stores Herdr container metadata; old tmux rows remain history.
3. **Require explicit user choice when no usable ref exists.** If the old tmux container is missing and no ref can be trusted, Kanbi should show repair/start-fresh language rather than silently launching a replacement.
4. **Start fresh means migration by new attempt.** Starting fresh deactivates the old active session, preserves history, sends the rendered ticket prompt, and creates a new active session in the currently configured multiplexer (Herdr after config change).
5. **Only mark old sessions closed/error explicitly or after validation.** Marking a tmux row closed is safe when the user requests it or runtime validation proves the container is gone. It should not fabricate Herdr metadata.

## Suggested TUI affordance

For tickets whose latest session is `tmux` while the configured default is `herdr`, surface a small migration hint on the card or repair modal:

```text
latest session: tmux · default: Herdr
```

Possible actions:

- **Open/keep tmux** — focus the valid tmux window; no DB session row is created.
- **Move to Herdr** — available when the latest session is tmux and a harness session ref exists; gracefully closes the tmux window, then resumes the harness in a newly created Herdr pane/agent.
- **Resume in Herdr** — available only when a harness session ref exists and there is no valid tmux window to keep; creates a new active Herdr session row with real Herdr container metadata.
- **Start fresh in Herdr** — always available from repair/migration UI; creates a new active attempt with the ticket prompt and preserves history.
- **Mark old tmux session closed** — explicit repair action for stale rows after the user confirms the tmux process is gone.
- **Edit ref, then resume in Herdr** — for older rows whose refs were missing or captured incorrectly.

The modal should avoid saying “convert session”. Preferred wording is “move to Herdr” for the explicit close-then-resume operation, or “resume/start a new Herdr attempt” when no live tmux window is being closed.

## Suggested CLI affordance

Add a repair-style command rather than an automatic database backfill, for example:

```bash
kanbi sessions migrate T-001 --to herdr --mode resume --board NAME
kanbi sessions migrate T-001 --to herdr --mode fresh --board NAME
kanbi sessions repair T-001 --mark-closed --board NAME
```

Expected behavior:

- `--mode resume` checks the latest session, requires a non-empty harness ref, validates that a live Herdr container is created, then writes a new active Herdr session row.
- `--mode fresh` starts a fresh Herdr attempt with the rendered prompt and preserves prior rows.
- `--mark-closed` marks the latest stale tmux row inactive/closed without launching anything.
- `--dry-run --json` should report the planned action, old multiplexer, configured default, whether the tmux container validates, and whether a harness ref is present.

## Safe automation boundaries

Safe to automate:

- Detect and label mismatch: `latest session is tmux; configured default is Herdr`.
- Validate whether the recorded tmux container still exists using the stored tmux session/window id/name.
- If the tmux container is missing and a ref exists, use the normal resume path to create a Herdr session.
- If validation proves the active tmux container is missing, mark that latest row inactive/error with an explanatory reason.

Must remain explicit:

- Replacing a valid active tmux window with Herdr.
- Starting fresh when a resumable ref exists but may not contain the desired context.
- Editing or trusting a user-provided/legacy harness ref.
- Any DB rewrite that claims an old tmux row is Herdr-owned without a real Herdr pane/agent id.

## Test coverage expectations

Before adding migration UI/CLI, keep deterministic tests around these lifecycle choices:

- With Herdr as default, a valid active tmux session is focused/kept and no replacement session is created.
- With Herdr as default, a stale tmux session with a stored harness ref resumes through Herdr and stores Herdr container metadata in a new active session row.
- A stale tmux session without a ref enters repair/start-fresh rather than being backfilled or silently relaunched.
