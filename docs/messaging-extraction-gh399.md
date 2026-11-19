# GH-399: Messaging extraction assessment

Implementation update (2026-11-14): the user approved proceeding with extraction
under the name [Poke](https://github.com/carlotran4/poke). The original assessment
below is retained as decision history. Kanbi now embeds Poke's mailbox and native
adapters while retaining its host policy, ordered migrations, Operator transactions
and CLI compatibility. See [session messaging](session-messaging.md) for the
current implementation contract.

## Recommendation

Extraction is feasible and worth a bounded experiment. The highest-value product
is a small, embeddable **durable mailbox with native harness notification adapters**,
with a JSON CLI for non-Go consumers. First establish that boundary inside Kanbi,
then prove it with a second, minimal host before creating a separately released
project. This document proposes future work; it does not authorize a scope change
or implement extraction.

The useful promise is: persist an addressed message, notify the correct existing
conversation when supported, and report precisely what is known about delivery.
The difficult reusable work is conversation identity, recipient environment,
crash fencing, native receiver setup, and honest receipts. A generic message queue
alone would discard much of that value.

## What exists and what must move

The directly named mailbox implementation is roughly 1,250 lines including the Pi
extension and CLI, plus roughly 760 lines of tests. These are orientation numbers,
not an extraction estimate: lifecycle, scheduling, prompts, migrations, and real
harness verification also participate.

| Source | Reusable behavior | Current coupling |
| --- | --- | --- |
| `internal/storage/messages.go` | Idempotent sends, inbox, reply routing, ack, bounded bodies/chains | SQL joins to sessions, boards, tickets and Operators; signed numeric addresses |
| `internal/storage/message_delivery.go` | Atomic notification claims, uncertainty fencing, retry and read receipts | Eligibility and runner-liveness checks embedded in SQL |
| `internal/runtime/message_delivery.go` | Claude socket framing, Codex queue submission, private endpoint assets | Manager, runtime locks, Herdr container validation, board state, Operator notices |
| `internal/runtime/message_launch.go`, `messages.go` | Per-launch receiver installation and identity/environment setup | Kanbi executable, database, session refs and command conventions |
| `internal/runtime/pi_message_extension.mjs` | In-process polling, native follow-up, editor/turn guards and reconciliation | Kanbi environment names and CLI protocol |
| `cmd/kanbi/messages.go` | Agent-friendly JSON commands and participant checks | Store initialization, session lookup, Operator remapping and configuration |

This is not a directory move. Operator scheduling directly inserts and updates
mail rows in `internal/storage/operators.go`; cancellation and wake completion
depend on those transactions. Runtime notification is serialized against
close/fresh/Fleet operations. Replacing these with a remote service would add
distributed consistency problems to currently local transactions.

## Proposed boundary

Keep message data, idempotency, reply authorization, receipt transitions and
notification claims in the reusable core. Support SQLite first. Allow the host
to supply its existing database/transaction so Operator wake creation and mail
insertion remain atomic; also offer a standalone database for other consumers.
Document ownership of schema migrations and transaction lifetime explicitly.
Retain Kanbi's existing rows and IDs through an adapter rather than migrating
history to a new service.

Use opaque, namespace-scoped endpoint addresses in the public contract. Distinguish
a stable logical address from a bound process/conversation generation. Kanbi maps
ticket attempts and `operator:ID` onto that contract; its negative-ID encoding
does not become a public convention. Old generations must never claim mail or
retarget a replacement conversation silently.

The host owns endpoint discovery metadata, active/paused/archived policy, verified
native refs, launch/close/resume, runtime locking and scheduling. Endpoint binding,
eligibility validation and notification claims need a documented atomic/fenced
contract: a loose `isActive()` callback would introduce a close/rebind race.
Operator instruction reload and automatic resume remain Kanbi policy.

Adapters own native registration, launch assets and bounded notification attempts.
Their result vocabulary must distinguish deferred-before-send, uncertain,
submitted and confirmed native acceptance. Persist before submitting; fetching
mail proves reception, acknowledgment means responsibility, and neither means
task completion. Preserve sender-scoped retry keys and explicit ambiguous retries;
do not promise exactly-once model execution.

Expose a Go API plus a versioned JSON CLI. The host can drive a bounded `drain`
operation from its own event loop; Pi can retain its in-process receiver. That
supports other languages without requiring a broker, daemon, MCP server or Herdr.
Copilot terminal delivery should be an explicit optional host adapter with its
existing draft limitation. Missing native capabilities should remain inspectable
deferred delivery, not trigger a hidden terminal fallback.

The initial trust boundary remains cooperating processes under one OS account.
Environment identity is not authentication; multi-user/remote operation would
require a separate security design. Keep native credentials private and outside
portable backups as today.

## Value versus alternatives

Public project documentation reviewed for this investigation:

- [MCP Agent Mail](https://github.com/Dicklesworthstone/mcp_agent_mail) offers
  identities, mail, searchable threads and file reservations. Its Python project
  now points to the maintained Rust rewrite.
- [Hardline](https://github.com/sushiHex/hardline-mcp) combines a durable SQLite
  mailbox with MCP, session lanes, wake delivery and delegation.
- [Open Agent Bridge](https://github.com/kevinArgueta96/open-agent-bridge) combines
  a shared registry with MCP and native agent adapters.

These descriptions establish overlap, not comparative correctness or a full
implementation audit. They suggest demand for coordination tooling, but do not
prove demand for another library. Kanbi's plausible niche is a narrow component
that an existing metaharness embeds while retaining lifecycle ownership, with
native delivery and a tested receipt contract. Avoid competing on orchestration,
file locking, hosted transport or another management UI. MCP can be a later thin
frontend if a consumer needs it; it does not itself solve native wake delivery.

## Staged investment and decision gates

1. **Internal boundary and contract tests:** separate core types/transitions and
   transport outcomes from Kanbi policy, retaining the database and command
   surface. Cover concurrent claims, crash/restart uncertainty, stale bindings,
   recipient-specific Codex homes, history retention and Operator transactions.
2. **Independent host:** build a tiny CLI example that registers two endpoints,
   sends/reads/acks/replies and drives notification without Kanbi or Herdr.
   Include deterministic fake transports and an opt-in actual native exchange.
   A second real metaharness integrator is the strongest validation of the API.
3. **Separate release only after that proof:** publish the Go module, CLI, adapter
   compatibility matrix and standalone example. Make Kanbi consume the same
   implementation rather than maintain a fork. Establish versioned CLI/schema
   compatibility and a native-harness regression policy before calling it stable.

Planning estimate, not measured delivery time: about 3–5 engineer-days for the
boundary and independent-host spike; roughly another 1–2 engineer-weeks for a
supported extraction with packaging, compatibility tests and Kanbi integration.
Native API drift and preserving transactional Operator behavior are the largest
uncertainties. Authenticated verification depends on installed versions/accounts.

Proceed to a separate project if the example works without Kanbi concepts, Kanbi
retains its safety properties, and a second consumer can integrate through the
documented contract. Otherwise retain the internal module and publish the design
and adapter examples. That still improves maintainability without committing to
another product's release and support burden.

## Evidence and limits

This assessment traces the current source and existing messaging documentation.
No runtime behavior, schema, session history or harness command was changed.
Existing documented evidence distinguishes authenticated Pi/Codex exchanges,
Claude's offline native fixture, and Copilot's unverified authenticated response.
Those are prior results, not native tests rerun for this investigation.

Focused verification passed with a writable build cache:
`GOCACHE=/tmp/kanbi-gh399-go-cache go test ./internal/storage ./internal/runtime ./cmd/kanbi -run 'Message|Native|Notification' -count=1`.
The initial default-cache attempt failed because the sandbox mounts that cache
read-only. `git diff --check` also passed. Full fmt/test/vet/smoke and real harness
checks were not run for this documentation-only assessment.
