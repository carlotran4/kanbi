# Runtime refresh investigation (GH-389)

The reported warning is `runtime refresh stale: <error>`. The original report
has no exact suffix, timing, versions, or reproduction. Its specific recurring
trigger is still unconfirmed; the changes below fix confirmed shared diagnostic
handling defects rather than claiming a Claude-specific failure.

## Confirmed behavior and defect

The TUI polls two seconds after the preceding observation completes. Each refresh
has one 1.5-second deadline covering runtime-lock acquisition, workspace/session
observation, writes, and board/focus reads. Workspace observation alone can use
up to 750 milliseconds. Lock contention, slow Herdr commands, database contention,
or enough sequential observations can therefore exhaust that shared budget.
These are possible triggers, not evidence of the original user's cause.

Herdr native detection is preferred; unknown/unavailable detection falls back to
agent output, then pane output. Previously:

- Generic transcript-read failures were saved on the session but refresh returned
  success, so the TUI could clear its stale warning despite failed observation.
- If the polling context expired, saving that failure used the expired context.
  The write error replaced the original observation diagnostic.
- Herdr command error text was omitted from native-detection and final pane-read
  failures, often leaving only an exit status.

These paths are shared by Claude, Codex, Pi, and Copilot. Successful refresh
already clears the TUI warning; no retained-warning defect was found in that
handler. Results from an obsolete board/filter generation are intentionally
ignored.

## Deterministic reproduction and correction

`TestRefreshRuntimeObservationFailurePreservesAttemptAndRecovers` creates an
active attempt for each harness, makes native detection unknown, and fails both
transcript reads. Before the fix refresh incorrectly returned success. It now
returns the ticket/session/harness and failing operation while keeping the same
active attempt, runtime state, resume ref, and agent. Other tickets can still be
observed. Restoring native observation makes the next refresh succeed and
replaces the session's failure reason.

`TestRefreshRuntimeCanceledObservationKeepsDiagnosticAndAgent` cancels the poll
inside its first Herdr command, after database reads. The returned error retains
the observation operation and cancellation cause without attempting a diagnostic
write on the canceled context. The agent remains active.

`TestObservationFailuresRetainHerdrDiagnostics` verifies the native error code and
pane-read error text survive. Persisted observation diagnostics and the displayed
warning are redacted. Operation labels also distinguish runtime lock, readiness,
workspace/session lookup, and observation-write failures. Known container absence
continues to follow the existing exited/repair-needed policy.

Existing TUI coverage
`TestRuntimeRefreshRejectsOldProjectionAndRetainsLastGoodBoard` verifies that a
failed refresh preserves the last valid board and the next successful refresh
clears the warning. Observation performs no agent start, input, or close operation.

## Real Claude check and remaining evidence

Herdr 0.9.3 was available locally. The opt-in disposable Claude lifecycle test
launched an actual Claude process, but its isolated XDG configuration encountered
the first-run theme/onboarding screen. Herdr `agent get` and transcript reads
succeeded there. Initial prompt submission then timed out waiting for agent
status (`herdr submit prompt`, `cli:agent:wait`); the test cleaned up its owned
server and retained artifacts. This does not verify authenticated active work,
resume, or exit.
No claim is made that the user's recurring warning has been reproduced.

Capture the full redacted warning on a recurrence, together with Kanbi, Herdr,
and Claude versions, number of active sessions/worktrees, and whether it happens
at launch, during work, idle, resume, or exit. The new operation labels distinguish
observation errors from runtime-lock and persistence failures without terminating
the agent or replacing its history. A successful subsequent refresh automatically
clears the warning; recurring failures now remain visible instead of falsely
reporting recovery.
