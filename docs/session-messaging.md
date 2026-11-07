# Cross-harness session messaging

Kanbi provides one durable mailbox across Claude Code, Codex, Pi, and Copilot.
New managed sessions automatically receive notification support. Agents send and
reply through the same CLI; no additional framework or separately installed
daemon is required.

```sh
kanbi messages recipients
kanbi messages send 42 --key schema-ready-v1 --body 'The schema is ready.'
kanbi messages inbox
kanbi messages ack 17
kanbi messages reply 17 --key implementation-ready-v1 --body 'Implementation updated.'
kanbi messages status 17
```

Send commits first, then makes a bounded attempt to enqueue a notice. It never
waits for the receiving model's response. The notice contains a message ID, sender
ID, and inbox/reply instructions; the untrusted message body remains in SQLite
until the recipient retrieves it. Read receipts do not acknowledge responsibility
or claim the requested work succeeded. No component generates automatic replies.

## Delivery mechanisms

| Harness | Automatic delivery | Limits |
| --- | --- | --- |
| Claude Code | A bundled, per-launch plugin registers the exact native inbox socket and token during `SessionStart`. Kanbi posts a fixed notice to that socket. | Native inbound `hold`/`refuse` and the session's permissions remain authoritative. The socket write is only a submission; fetching the inbox or acknowledgement proves reception. A missing endpoint is deferred, not replaced with terminal input. |
| Codex | `codex queue --thread <verified native ref> --message <notice>` queues a notice into the existing conversation. | Requires native queue support and a verified ref. The recipient's executable, search path, and `CODEX_HOME` are recorded at launch and used by the notifier, independent of the sender's environment. Kanbi never uses `queue --resume` or creates a second process running the conversation. |
| Pi | A bundled extension is loaded on start **and resume**, including normal interactive sessions. It polls every two seconds inside the existing Pi process and calls native `sendMessage` with `triggerTurn` and `followUp`. | Checks native idle/pending-message state and an empty interactive editor, then rechecks after asynchronous I/O. Typed drafts defer notification. Native conversation changes disable the old address. Persistent native entries reconcile notification IDs after interruption. |
| Copilot | Validates the recorded Herdr container, pastes a fixed inbox notice, and sends Enter. | Best effort, explicitly using terminal input. Known busy/permission states defer while Kanbi is open. Draft preservation and native acceptance are not guaranteed; the text can combine with an unsent draft. Submission is not treated as acknowledgement. |

Immediate send/reply notification and release of queued notices on acknowledgement work without the board UI. The existing
runtime-refresh loop retries deferred deliveries while Kanbi is open; Pi's
receiver continues independently inside its harness. If the board has exited,
a busy Copilot's deferred mail remains queued until runtime refresh resumes or
another send retries its inbox. No detached notifier is installed.

Already-running sessions need a normal close/reopen to acquire the new receiver
setup. Custom wrapper commands that hide a native executable may not support
automatic setup: inspect `notification_error` instead of assuming delivery.
The supported start/resume command names and Herdr ownership are unchanged.

## Identity and addressing

Each launch supplies `KANBI_SESSION_ID`, absolute `KANBI_DB`, and the exact
`KANBI_MAILBOX_CLI` executable. Canonical Codex also receives session/database
identity through explicit per-thread shell environment policy because TUI process
environment alone does not reliably reach daemon-owned tools.

Ticket-session addresses are session-attempt IDs **in this database**, not globally portable
identities or board-local ticket IDs. Discovery includes board, ticket, harness,
active/paused/archive state, and delivery method. A declared method does not prove
an older running session has registered its receiver. The message's notification
status/error records actual progress and missing capability.

There is no sender argument: Kanbi supplies identity at launch. Replies supply a
parent message ID and Kanbi derives the original sender. Only the recipient may
reply or acknowledge. Self-send is rejected and reply chains stop after eight
hops. Local environment identity is process context, not authentication against
mutually hostile processes sharing the same OS account and database.

Closed/paused/archived recipients retain queued mail and are never automatically
resumed. Missing recipients are rejected; correspondence with subsequently deleted
endpoints remains inspectable by surviving participants. Normal resume/start-fresh
creates a new attempt address: history is not silently forwarded. Historical inbox
inspection may explicitly use the old `KANBI_SESSION_ID` with the same database.
A Claude `/clear` or Pi conversation change cannot silently rebind an old attempt.

## Durable states and retry

Message state is `queued`, `notified`, or `acknowledged`. Notification progress is
recorded separately:

- `pending`: committed, awaiting notification.
- `deferred`: no notification was submitted; busy state, missing ref, or missing
  endpoint is reported in `notification_error` and may be retried automatically.
- `uncertain`: a claim was committed before transport submission. The operation
  may still be in flight or may have reached the recipient. A crash retains this
  state; another process cannot silently repeat it.
- `submitted`: a Claude socket write or Copilot paste/Enter completed. The message
  remains queued until the recipient fetches it; this is not model acceptance.
- `notified`: native Pi context/Codex queue accepted the notice, or the recipient
  fetched the body. Only explicit `ack` changes message state to `acknowledged`.

One unconfirmed handoff per recipient fences uncertain/submitted deliveries.
Native acceptance or fetching the inbox releases subsequent handoffs without
waiting for task completion or acknowledgement. Native Pi entries can reconcile an uncertain
claim without replay. Confirmed pre-submission socket connection failures are
safe to defer; failed/partial writes and ambiguous native command failures remain
uncertain. Fetching `inbox` receipts returned messages but never acknowledges work.
`inbox --all` also includes acknowledged history. Reads are bounded to 100 records;
acknowledging advances the pending inbox. `status` reads an exact ID.

Identical sends with the same sender-scoped `--key` return the existing message.
Changed content/routing with that key is rejected, including concurrent processes.
Bodies are limited to 16 KiB and retry keys to 128 bytes. A transport retry is a
separate action:

```sh
kanbi messages retry MESSAGE_ID
```

Only a participant may explicitly retry an uncertain queued notification older
than 30 seconds. Inspect the recipient first: this deliberate retry can duplicate
a notice whose earlier acceptance was unknown. It keeps the durable message row and ID.
An acknowledgement is evidence of responsibility, not task completion.

Migrations 11–12 retain messages and notification receipts in SQLite. Full database
backup includes them; portable board packages exclude the cross-board mailbox.
Endpoint credentials live in private `<database>.messages/` files, outside database
backups, with private directories and mode-0600 atomic files. Session history is
preserved. Board deletion does not cascade into the other participant's mail.

## Verification and evidence

Normal tests cover concurrent claims across independent database connections,
restart fencing, explicit retry restrictions, monotonic receipts, recipient checks,
Claude socket frames, Copilot busy/permission deferral, exactly one paste/Enter,
recipient-specific Codex homes, command preservation, and private receiver assets.

```sh
go test ./internal/harness ./internal/runtime ./internal/storage ./cmd/kanbi
python3 scripts/prototypes/pi-mailbox-smoke.py
KANBI_REAL_HARNESS_TESTS=1 python3 scripts/prototypes/real-session-messaging.py
KANBI_NATIVE_CLAUDE_TESTS=1 python3 scripts/prototypes/claude-mailbox-smoke.py
```

The Pi smoke uses an installed native engine with a private offline fixture model.
The authenticated Pi/Codex test uses ordinary interactive managed sessions and
production notification; its driver does not inject wake notices for the main
exchange. It checks sender tools, automatic idle wake, inbox/ack/reply, busy Codex
delivery, Codex draft preservation at 160x45, and Pi draft deferral at 80x24.
Optional `KANBI_REAL_COPILOT_TESTS=1` adds the installed Copilot fallback check.

The Claude smoke uses the installed CLI, an owned native session, and a localhost
Anthropic-protocol fixture. It requires no Claude account and is distinguished
from authenticated provider verification. Production inbound settings are never
weakened; `accept`/bypass settings in that test apply only to its offline fixture.
Exact results and unresolved environment blockers belong in the change report.

Native references: [Claude inbox socket](https://code.claude.com/docs/en/cross-session-messaging#the-sessions-inbox-socket),
[Claude hooks](https://code.claude.com/docs/en/hooks#sessionstart),
[Pi extensions](https://github.com/earendil-works/pi-mono/blob/main/packages/coding-agent/docs/extensions.md),
[Codex queue](https://github.com/openai/codex/blob/main/codex-rs/cli/src/queue_cmd.rs).

## Reuse decision

The investigation reviewed claude-code-tools msg, Hardline MCP, Agent Deck,
cross-agent_mcp, Agent Exchange, and Agent Intercom. Kanbi keeps its existing
SQLite/session/Herdr infrastructure and implements the small native delivery
adapters directly. No messaging framework or external broker is a dependency.
MCP remains optional future tooling, not a prerequisite for wake delivery.

Verified locally for this change: the authenticated interactive Pi/Codex production
exchange, busy Codex delivery, Codex draft preservation, and actual Pi PTY draft
deferral at 80x24/160x45 passed. Installed Claude 2.1.319 passed SessionStart
registration, native socket wake, and real Bash inbox/ack against the offline
local provider fixture. Copilot 1.0.75 reached its interactive UI but its model
catalog returned HTTP 403 from account/organization policy, so authenticated
Copilot response/ack remains unverified here. Its exact validated paste/Enter and
busy/permission deferral are covered by deterministic tests. Full tests, focused
race checks, lifecycle/smoke, UI smoke, and the same-tree backup/restore drill passed.

Workspace [Operators](operators.md) extend this same mailbox with stable `operator:ID`
addresses (negative IDs in JSON). Operator recipients survive session close/fresh,
automatically resume through the board-owned worker, and require current instructions
on every wake. Ticket-session addressing and closed-recipient behavior above remain
unchanged. Deleted Operator addresses retire while retaining correspondence.
