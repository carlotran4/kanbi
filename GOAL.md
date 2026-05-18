# GOAL
Implement Agent Kanban foundation, static TUI board, and tmux session/window
 orchestration from design-spec.md.

   Read these files first:
   - design-spec.md
   - docs/autonomous-verification.md
   - README.md

   Objective:
   Build a working local MVP skeleton of Agent Kanban with SQLite persistence, a Bubble Tea
 Kanban board, and real tmux-backed ticket windows. Use fake harnesses/smoke commands for
 verification instead of real agent CLIs.

   Scope:

   1. CLI/config/storage
   - Implement the `agent-kanban` CLI.
   - Add subcommands:
     - `agent-kanban`
     - `agent-kanban doctor`
     - `agent-kanban add "title" --body "..." --harness pi`
     - `agent-kanban list`
   - Implement XDG config/data/state path resolution.
   - Implement YAML config loading with defaults.
   - Implement SQLite schema/migrations for boards, columns, tickets, and sessions.
   - On first run, create one default board with columns: Open, In Progress, Review, Done.
   - Implement ticket creation/listing with sequential display IDs like T-001.

   2. Static Bubble Tea board
   - Implement a TUI board that loads columns/tickets from SQLite.
   - Display compact cards with:
     - display ID
     - title
     - harness
     - tmux window indicator placeholder/real indicator where available
     - runtime state
   - Implement keyboard navigation:
     - h/j/k/l and arrows move focus
     - H/L move selected ticket between columns
     - J/K reorder ticket within a column
     - n creates a title-only ticket in the current column with default harness pi
     - e edits ticket title/body/harness
     - a archives selected ticket
     - q quits
   - Launching from the TUI can be minimal if needed, but service methods for open/switch
 ticket session should exist.

   3. Tmux manager
   - Implement a tmux manager that:
     - ensures dedicated tmux session `agent-kanban`
     - ensures board window `board`
     - creates one tmux window per active ticket
     - names ticket windows like `T-001-title-slug`
     - switches to existing ticket windows
     - renames ticket windows when ticket title changes
     - records tmux session/window metadata in SQLite sessions
     - reconciles missing tmux windows on startup
   - If `agent-kanban` is run outside tmux, auto-create/attach to the dedicated tmux session
 using an inner env guard like `AGENT_KANBAN_INNER=1`.

   4. Fake/smoke harness support
   - Do not integrate real `pi`, `codex`, or `gh copilot` behavior yet.
   - Add test/smoke harness scripts under a safe test path, e.g. `scripts/fake-harnesses/`.
   - Fake harnesses should simulate:
     - start
     - prompt-ready output
     - session ref output
     - resume with explicit session ref
     - waiting for user
     - permission prompt
     - graceful exit if feasible
   - Add a config or env-supported way for smoke tests to point harness commands to fake
 harnesses.

   5. Prompt rendering and tmux paste
   - Implement first prompt rendering:
     # T-001: Ticket title

     Ticket body
   - Implement tmux paste-buffer prompt injection into a ticket window.
   - Add a timeout for prompt-ready detection; if not ready, fail gracefully or skip paste in
 smoke mode.

   6. Tests
   - Add tests for:
     - config defaults/overrides
     - XDG path resolution
     - SQLite migrations/default board
     - ticket display ID generation
     - ticket create/list/archive
     - ticket move/reorder
     - prompt rendering
     - tmux command construction
     - tmux manager behavior where possible
     - fake harness command construction

   Do not implement:
   - real Pi/Codex/Copilot detection logic
   - real harness session-ref parsing beyond fake harness path
   - runtime watcher/polling
   - flashing based on process output
   - auto-close
   - background daemon
   - full transcript storage
   - custom user-defined harness adapters
   - multi-board UI

   Validation:
   Run:
   - go fmt ./...
   - go test ./...
   - go vet ./...

   Also create and run a smoke script, e.g.:
   - ./scripts/smoke.sh

   Smoke script should:
   - use temporary AGENT_KANBAN_DB/config/state paths
   - point harness commands at fake harness scripts
   - run `agent-kanban doctor`
   - add a ticket
   - list tickets
   - create/verify the dedicated tmux session
   - create/verify a ticket tmux window
   - inject a prompt into the fake harness using tmux paste-buffer
   - capture pane output to verify the fake harness received the prompt
   - clean up the test tmux session afterward

   Stop when:
   - the app has working persistent board data
   - the TUI can display and manipulate tickets
   - tmux session/window creation works
   - fake harness prompt injection works through tmux
   - smoke test passes
   - all validation commands pass
   - any incomplete TUI niceties are documented in PROGRESS.md

   Work in checkpoints and keep a short progress log in PROGRESS.md. If blocked by ambiguous
 product decisions, pause and explain the decision needed.
