# Scheduled ticket prompts

`W` on a ticket in a named board or Master opens its scheduled prompts. `n`
creates a draft, `Tab` switches between time and multiline prompt, and `Ctrl+S`
queues it. `Esc` discards the draft. Time defaults to tomorrow at the current
local hour/minute. Enter `YYYY-MM-DD HH:MM` in the displayed machine-local zone,
or RFC3339 with an explicit offset for an unambiguous instant around daylight
saving changes. Only future times and nonblank prompts up to 16 KiB are accepted.

The list remains bound to the ticket it opened for, including across runtime
refreshes. Up/Down selects, `r` refreshes status, and `x` cancels a pending/failed
prompt or a mailbox notice still safely unsent. Submitted, uncertain and accepted
notices cannot be cancelled. Scheduling multiple prompts retains their separate
contents and identities. Cancelling a prompt does not close its agent.

SQLite migration 14 stores ticket-owned `ticket_wakeups`. The existing joined
in-process scheduling worker checks due work across all boards, using the shared
runtime lock and an atomic pending-to-activating claim. Kanbi must remain open;
there is no OS timer or detached service. After downtime, pending prompts become
eligible at the next launch. Paused tickets and archived tickets/boards retain
pending work until available again. The scheduler does not unpause work.

Activation uses normal lifecycle policy without changing user focus. A ticket
with no attempts starts with its normal ticket prompt/context. An existing valid
agent is reused; inactive conversations resume only with verified refs. Worktree
setup requiring a branch choice, missing refs, runtime errors and failed resumes
surface as failed scheduled prompts. Repair/open the ticket normally, then queue
the prompt again. No automatic fresh conversation or failed-launch retry occurs.
An interrupted activation is also failed for inspection, rather than risking a
repeated launch. Ticket and session history remain intact.

After activation, a single system-authored mailbox message and the queued state
commit atomically. Existing native transports deliver the follow-up under normal
harness permissions. Delivery statuses retain the mailbox's pending/deferred,
uncertain, submitted and notified meanings; notified confirms transport acceptance,
not completion of the requested work. Uncertain delivery requires inspecting the
session before an explicit mailbox retry. Copilot retains its documented
best-effort terminal delivery/draft limitation. Old sessions lacking native inbox
support may defer delivery until reopened with this version.

Once queued, the prompt belongs to that exact session attempt. Closing or starting
fresh does not forward mailbox history; cancel a safely unsent prompt and queue
it again to target the new conversation. The list exposes the notification status
and errors, and retains cancelled/failed entries. Full database backups retain
schedules and messages. Board packages do not transfer this machine-local
automation, so importing a board cannot unexpectedly replay scheduled prompts.
