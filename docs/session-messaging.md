# Cross-harness session messaging (GH-396)

Status: durable common CLI and a verified, opt-in Pi/Codex end-to-end prototype.
An authenticated exchange, idle activation, Codex busy deferral, and preservation
of an unsent Codex draft passed in disposable sessions. Copilot/Claude delivery,
permission-dialog safety, and default production notification remain blockers.
Normal managed sessions receive mailbox identity without starting a watcher or
automatically injecting terminal input.

## Recommendation

Keep one SQLite mailbox inside Kanbi and expose it through the CLI first. Bind
senders to the exact durable session attempt at launch. Keep notification adapters
outside storage: native acceptance is a separate event from committing mail, and
acknowledgement is an explicit recipient action. Prefer native queues/extensions;
do not reuse terminal paste for autonomous messaging.

The CLI is usable from all four harnesses without an additional server. MCP could
improve tool discovery, structured arguments, and permission configuration later.
An MCP connection alone does not provide push delivery or start a model turn.
Do not add a second mailbox database, detached daemon, or a new runtime substrate.
A production notifier should use an existing manager-owned loop or a receiver
extension whose lifetime is the harness process, with a versioned native contract.

`SendTicketMessage` remains an explicit pause-handoff operation: it sends literal
text and Enter through the stored container. It has no draft/dialog preflight,
mailbox, acknowledgement, or idempotency contract. Reuse Kanbi's session storage,
launch identity, verified ref capture, runtime locking, and container validation;
do not call that method to deliver queued mail automatically.

## Common interface and addressing

Newly launched managed sessions receive `KANBI_SESSION_ID` and `KANBI_DB`, including
resumes and custom commands. One `env` prefix preserves Herdr's native agent-start
path and the existing Pi ref-extension environment. Canonical Codex launches also
set these two values in the thread's explicit `shell_environment_policy.set`:
the live probe showed that TUI environment alone did not reach daemon-owned shell
tools. Database paths are made absolute before changing to the ticket cwd. Already-running sessions need
an explicit normal close/reopen to receive this environment; no live processes are
rewritten. Sender identity is supplied by Kanbi; there is no `--from` argument.

```sh
kanbi messages recipients
kanbi messages send 42 --key schema-ready-v1 --body 'The schema is ready.'
kanbi messages inbox
kanbi messages ack 17
kanbi messages reply 17 --key implementation-ready-v1 --body 'Implementation updated.'
kanbi messages status 17
kanbi messages inbox --all
```

All output is JSON. Send receipts include a durable message and a separate delivery
limitation (`native_adapter_required`, `paused`, `archived`, `starting`, `closing`,
`closed_explicit_resume_required`, `closed_unresumable`, `missing`, or `unknown`).
That limitation does not invalidate a committed message. IDs are SQLite session IDs **within this database**, not ticket
display IDs, native harness UUIDs, or globally portable addresses. Discovery includes
board, ticket, harness, active/paused state, and a delivery limitation. A stored ref
is labeled `has_session_ref`, not proof that a resume will succeed. Discovery is
bounded to the newest 1,000 attempts; inbox reads return the oldest 100 eligible
messages. Acknowledging advances the pending inbox. `status` reads an exact ID.

The receiving agent supplies only a parent message ID for a reply; Kanbi derives
the destination and reply depth. Only that parent's recipient may reply. Commands
validate membership before reading status or changing delivery state. The local
owner can override environment variables or edit SQLite; this is process context,
not an authentication boundary against mutually hostile local processes. An MCP
wrapper must preserve the exact session/database binding rather than infer it
from cwd, process recency, or a user-supplied sender label.

## Message lifecycle and retry

1. `send` atomically persists a message as `queued` and returns without waiting
   for a reply or attempting terminal delivery. Bodies are limited to 16 KiB;
   caller-supplied retry keys are limited to 128 bytes.
2. A native receiver calls `messages pending` to preflight its own active attempt.
   Paused, archived, starting, closing, and inactive endpoints return no pending
   notifications. It must independently know native turn/input safety; runtime
   `waiting_for_user` and `idle_unknown` are not sufficient authorization.
3. After evidence of native context acceptance, the receiver calls
   `messages notified MESSAGE_ID`. This changes `queued` to `notified` without
   consuming mail. A late notification never regresses an acknowledgement.
4. The recipient reads the body with `inbox` and explicitly calls `ack` after it
   has taken responsibility. `acknowledged` does not mean the requested work
   succeeded. Reading, notifying, and replying do not implicitly acknowledge.

The unique `(sender, request_key)` constraint serializes concurrent retries.
Identical retries return the original ID; changed content or routing is rejected.
Retries still find the receipt if the sender has since closed. Native adapters
must carry the message ID and deduplicate against native acceptance evidence.
A timeout is not proof of rejection; never automatically paste/resubmit an
ambiguous delivery. The Pi prototype leaves ambiguous notifications queued and
logs errors; it suppresses another submission in that process and reconciles
persisted native entries on subsequent polls. This is not a general exactly-once
model-execution guarantee.

No component generates automatic replies. Self-send is rejected, replies are
limited to eight hops, and notices tell agents not to answer acknowledgements.
These bounds do not prevent an agent from deliberately starting fresh threads;
a production Operator activation policy will also need explicit per-thread turn
budgets and rate limits before unattended deployment.

Closed recipients retain queued mail. Paused recipients are never automatically
resumed. A missing recipient is rejected at send; a subsequently deleted endpoint
leaves its correspondence inspectable by the surviving participant. Unresumable
recipients require human repair. A normal resume creates a new attempt under
Kanbi's existing lifecycle contract: messages and replies stay addressed to the
original attempt and are **not** silently forwarded. Historical inbox inspection
can explicitly use the old `KANBI_SESSION_ID` with the same database. Automatic
migration between attempts is intentionally unimplemented.

Migration 11 adds `session_messages`. Messages survive process/database reopen and
whole-database backups. The table retains endpoint IDs without cascading session
foreign keys so deleting one board does not erase another board's correspondence.
Portable single-board packages exclude this cross-board, machine-local mailbox;
imports receive new session identities. This change does not delete or rewrite
existing ticket/session history.

## Capability matrix

Evidence collected locally on 2026-11-07. “Help verified” establishes a command
surface, not successful message delivery. External project claims are separate
from tests against these installed versions.

| Harness | Installed evidence | Native notification / idle wake candidate | Busy, draft, and permission handling | Close / resume and remaining blocker |
| --- | --- | --- | --- | --- |
| Pi 0.84.2 | Installed extension/RPC docs inspected; offline native wake/persistence passed; authenticated Codex-to-Pi send, idle wake, inbox read, acknowledgement and reply passed | `sendMessage` with `triggerTurn: true`, `deliverAs: followUp`; opt-in prototype below | Prototype polls only when native `isIdle()` and no pending messages, then rechecks after CLI I/O. It requires RPC mode and refuses interactive mode; never calls editor or terminal APIs. Pi interactive drafts/dialogs and Pi busy deferral still need full live acceptance | Session ID changes stop receiver delivery. Durable native message IDs allow receipt reconciliation. Normal Kanbi interactive sessions do not automatically load this prototype |
| Codex 0.167.0 | Version/help verified; authenticated native queue wake, inbox read/ack, unsent-draft preservation, and delivery after an in-flight tool completed all passed | `codex queue --thread UUID --message NOTICE` verified in the finite prototype; app-server turn APIs remain another documented path | Native queue preserved a typed draft and allowed the original eight-second tool to complete before handling the queued notice. Permission-dialog behavior still needs a live test. No terminal injection used for notification | Use the exact verified stored ref and matching CODEX_HOME. `queue --resume` warns that failed resume can still exit successfully, so do not equate exit zero with activation. The finite test driver sends the native queue notice; no production notifier is installed |
| Copilot CLI 1.0.75 | Version/help verified, including `--acp`, remote controls, and bundled extension SDK option | Experimental extension SDK or an already-owned ACP connection; external AHP support is for VS Code, not proof for this CLI | Need a version-specific live test of idle activation, safe busy scheduling, input preservation, and permission handling on the existing interactive session | A second ACP process or `--resume` invocation is not proof of delivery to the existing pane. No verified native attachment path implemented |
| Claude Code | `claude` resolves to a mise launcher; `--version` cannot resolve its configured version because network resolution fails in this sandbox | Supported channels after explicit setup; Hardline also reports a session-owned native inbox | Need a runnable pinned version and owned session to verify channel/inbox binding, idle/busy delivery, drafts, and permissions | Launcher availability is not installed-version verification. No private inbox writes, invented refs, or speculative terminal delivery |

Official/native references: [Pi extensions](https://github.com/earendil-works/pi-mono/blob/main/packages/coding-agent/docs/extensions.md),
[Pi RPC](https://github.com/earendil-works/pi-mono/blob/main/packages/coding-agent/docs/rpc.md),
[Codex app-server](https://developers.openai.com/codex/app-server),
[Codex queue source](https://github.com/openai/codex/blob/main/codex-rs/cli/src/queue_cmd.rs),
[Copilot extensions](https://docs.github.com/en/copilot/tutorials/create-an-extension),
[Claude channels](https://code.claude.com/docs/en/channels).

## Prototype and verification

The normal CLI is the common mailbox interface. For a disposable headless Pi
experiment, launch Pi with `--mode rpc -e /absolute/path/to/scripts/prototypes/pi-mailbox.mjs`
and its exact managed `KANBI_SESSION_ID`/`KANBI_DB`; set `KANBI_MAILBOX_CLI` to the
built binary. Loading this file is explicit and does not install user config or
start a detached process. Do not attach it to an arbitrary live user session.
The native notice contains routing metadata, not the untrusted message body.
The model must run `inbox`, `ack`, and optional `reply` itself.

```sh
go test ./cmd/kanbi -run TestMessagesCrossHarnessMailbox -v
go test ./internal/storage -run TestSessionMailbox -v
python3 scripts/prototypes/pi-mailbox-smoke.py
```

The CLI test exchanges mail between synthetic Claude/Pi session records, reopens
the database, notifies, acknowledges, and replies. Storage tests cover concurrent
retry deduplication, changed-key rejection, participant checks, closed/paused/
missing recipients, no fresh-attempt forwarding, monotonic states, and hop limits.
The optional native smoke uses the installed Pi engine and an offline fixture
model with isolated config/history. It proves idle activation and native
notification persistence, then exercises CLI acknowledgement/reply. Its sender
is synthetic; **it does not satisfy the two-real-harness acceptance criterion**.
Normal Herdr smoke remains fake-harness lifecycle coverage. The opt-in existing
`KANBI_REAL_HARNESS_TESTS=1 KANBI_REAL_HARNESSES=pi,codex ./scripts/real-harness-lifecycle.sh`
also passed against authenticated harnesses (real response, verified ref, focus,
close/resume, preserved attempt history). Lifecycle success does not establish
messaging success.

`KANBI_REAL_HARNESS_TESTS=1 python3 scripts/prototypes/real-session-messaging.py`
is a separate finite authenticated exchange test. It owns a disposable Herdr
server and Kanbi database, starts the Pi RPC receiver and a normal Codex TUI,
and uses a single native Codex queue notice for the reply. It installs no daemon.
It may consume quota and uses existing native auth/history, like the lifecycle
script. The initial run exposed missing Codex shell identity; the explicit shell
policy fixed it. The subsequent authenticated exchange passed. Additional probes
typed `KANBI_UNSENT_DRAFT_396` into the owned Codex editor and verified it remained
a draft after native queue delivery, then queued a notice while an actual shell
tool slept for eight seconds and verified the original tool completed before the
queued response. These probes target only the fixture's exact owned pane.

Observed authenticated sequence: Codex committed message 1; idle Pi activated,
read and acknowledged message 1, and committed reply 2 with `reply_to=1`; Codex's
native queue activated the originating thread, which read and acknowledged reply
2. Both messages reached `acknowledged` without human relaying. The Pi executable
is pinned to the resolved installed path in the fixture to avoid mise selecting
a different version under isolated XDG paths.

Before enabling production automatic delivery, extend owned native tests to Pi
busy deferral, approval dialogs, process restart during notification, and explicit
message routing across resume. Add native Claude and Copilot delivery tests. Verify every acceptance item
using native conversation/turn evidence, not a successful send command alone.

## Existing projects: reuse decisions

- [claude-code-tools msg](https://pchalasani.github.io/claude-code-tools/tools/msg/)
  separates durable mail from wake notices and combines a watcher with hooks.
  Adopt that separation and fixed inbox notices. Its agent/prompt assumptions and
  tmux-oriented watcher are not a Herdr safety contract; do not import the watcher.
- [Hardline MCP](https://github.com/sushiHex/hardline-mcp) and its
  [inbox-signal design](https://github.com/sushiHex/hardline-mcp/blob/main/docs/inbox-signals.md)
  distinguish wake receipts from mailbox reads. Its server wires mailbox and
  native adapters separately. Adopt receipt-based capability reporting and
  investigate its Codex queue approach. Do not duplicate its lane database or
  assume its private Claude inbox path works with an unverified local version.
- [Agent Deck's communication contract](https://github.com/asheshgoplani/agent-deck/blob/main/skills/agent-deck/references/session-communication.md)
  distinguishes durable queueing, submission, and transcript confirmation.
  Adopt observable delivery evidence. Its tmux input and notification daemon are
  not drop-in implementations for Kanbi; submission is not acknowledgement.
- [cross-agent_mcp](https://github.com/whooperlove/cross-agent_mcp) distinguishes live
  protocol injection from a separate resumed CLI. Adopt exact session/turn
  correlation and explicit unsupported results. Its panel shims and VS Code
  Copilot AHP path do not establish a path into Herdr-managed CLI sessions.

Implement storage, addressing, and reply policy in Kanbi. Reuse supported native
harness interfaces and verification patterns, rather than adding any of these
projects as a runtime dependency. No external implementation code was copied.
