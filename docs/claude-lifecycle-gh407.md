# Claude Code lifecycle through the Codex bridge

On 2026-11-18, Anthropic's npm `stable` tag returned `2.1.311` and the
disposable install reported `2.1.311 (Claude Code)`. The `latest` tag returned
`2.1.328`. Herdr was `/usr/bin/herdr` 0.9.3 and the checksum-verified bridge
was `claude-codex` 0.8.0. The live model was `gpt-6.1-sol`, using the
existing Codex login with the bridge bound to `127.0.0.1`.
[Anthropic registry tags](https://registry.npmjs.org/-/package/@anthropic-ai%2fclaude-code/dist-tags)
[Bridge release](https://github.com/fcakyon/claude-code-with-codex/releases/tag/v0.8.0)

The disposable Kanbi source was `a1e8af5dcc2012230b2ecfd61f746d188250acdd`.
It contains `47ce5f5`, `1eb6c65` and the worktree branch option in `a1e8af5`.
The GH-407 branch head at the start of testing lacked the latter two commits.

## Reproduction and results

1. Use a private Claude config with onboarding complete and trust granted for
   the fixture project. Install only the Claude Herdr integration into that
   config. Point Claude's base URL at the loopback bridge and set the model
   explicitly. The isolated Claude client used a fixture-only auth placeholder
   to enable the custom endpoint. Run
   `KANBI_REAL_HARNESS_TESTS=1 KANBI_REAL_HARNESSES=claude ./scripts/real-harness-lifecycle.sh`.
2. In the disposable Herdr PTY, alternate 80×24 and 160×45. Add five unique
   tickets per startup mode and use plain `kanbi open`. In the delayed mode
   sleep eight seconds in the launcher before executing the pinned Claude
   binary. Require a rendered assistant reply and one matching user turn in
   the native transcript. All twenty transcripts across the two passes also
   contained exactly one `api-request` record and one assistant turn.
3. Reopen each active ticket and check its session row and pane identity.
   Close the pane externally and reopen. Require the same native ref, the
   rendered reply from history, two attempts, and exactly one active attempt.
   Repeat with the GH-407 patch applied to the disposable source. Before each
   active reopen, focus a different tab in the same workspace and assert that
   `herdr pane current` returns the original Claude pane afterward.

| Scenario | Result |
| --- | --- |
| Repository's Claude-only suite at 160×45 | Passed launch, model reply, ref capture, active reopen, external exit and native resume |
| Warm launches | 5/5 passed on both the baseline and patched snapshot, three at 80×24 and two at 160×45 per pass |
| Eight-second delayed launches | 5/5 passed on both the baseline and patched snapshot, three at 80×24 and two at 160×45 per pass |
| Fresh Claude config with no Herdr hooks, 80×24 | 1/1 passed using history ref fallback and native resume |
| Dropped bridge connection, 80×24 | 1/1 passed after two fixture TCP resets. Claude retried after the relay reopened. The transcript had one matching user turn and the original attempt stayed active. |
| Trust gate and recovery, 80×24 | Cleared trust, observed the actual trust dialog, and verified no prompt in native history and an inactive error attempt. Granted trust in the private fixture config, opened ticket repair, and chose Start fresh. The new attempt sent the marker once and preserved the error row. |
| Graceful close, 80×24 | Pressed `x` on the recovered ticket in Kanbi. Its pane closed, the attempt became inactive, and the next open resumed the marker under the same native ref. The database retained three attempts including the earlier startup failure, with one active. |
| Injected prompt-acceptance timeout, 80×24 | 1/1 passed with the GH-407 patch. The Herdr CLI shim returned a timeout after the real submission. Kanbi kept the pane active, the single model reply appeared, focus returned to the same pane, and native resume preserved the turn. |

## Sanitized failure excerpts

- `The 'gpt-5.5' model is not supported when using Codex with a ChatGPT account.`
  The loopback bridge returned HTTP 400. A short `gpt-6.1-sol` preflight and
  the subsequent real Claude model turns succeeded.
- `● KANBI_<marker>` was visible and present in the Claude transcript, while
  a screen assertion accepting only `•` timed out. The lifecycle suite now
  accepts either Claude reply glyph and still requires the marker on its own
  reply line.
- With trust cleared in the private Claude config, Herdr returned
  `agent_not_ready: agent [redacted] is blocked during startup and is not ready
  for prompts`. Another launch reached Kanbi's editor readiness deadline.
  The failed attempt had no recorded user prompt. A plain reopen on the failed
  attempt returned `ticket has no active container and no session ref is known`.
  The Herdr startup error now explains how to review the startup prompts and
  then start fresh. The editor deadline still reports a generic timeout.
  The fake API key did not independently reproduce the approval screen in this
  fixture. Trust provided the reproduced approval gate.
- Once, Herdr returned `timeout: timed out waiting for agent status` after a
  stalled prompt submission, even though Claude recorded one user turn and one
  model reply. Kanbi marked the attempt as an error and closed the pane. The
  regression test exercises that timeout and verifies the pane stays active
  when the persisted prompt and the pane's native session ref agree. A
  different transcript ref still produces an inactive error and pane cleanup.
