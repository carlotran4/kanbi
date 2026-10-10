# GH-376 bug hunt

Three `gpt-6-luna` agents exercised current source in separate disposable Herdr
fixtures. No production database, configuration, or authenticated harness was
used. Findings below describe observed behavior; this change does not implement
fixes. The subsequent fix and validation are recorded below.

## P2: Pause checkpoint arrow keys leave multiline fields

The Focus Mode pause form accepts multiline text, but Up/Down cycle form fields
instead of moving the text cursor between lines. This makes editing a multiline
checkpoint awkward. Text is retained; no data loss was observed.

Reproduce with the project UI-validation helper:

1. Start a fresh fixture with a unique `KANBI_UI_WORKDIR` under `/tmp`.
2. Select Master with Enter, then use `b`, Down, Down, Enter to select Default.
3. With the initial T-004 selected in Open, press `L` to move it into In Progress.
   The seeded Focus usage is 4/3, so a replacement chooser opens.
4. Press Enter on T-005 to open its pause form.
5. In Why, type `line one`, press Enter, and type `line two`.
6. Press Up.

Expected: the cursor moves from the second line to the first in Why.

Actual: focus moves from Why to Exact next action, wrapping backward through the
three fields. Down likewise moves to the next field.

Cause: `internal/tui/focus_modal.go`, `updatePause`, intercepts `down` and `up`
alongside Tab and Shift+Tab before dispatching input to the textarea. Tab and
Shift+Tab already provide field navigation.

The agent reproduced the issue in the real application at 160×45 and exercised
the surrounding pause flow at 80×24. Local captures were retained at:

- `/tmp/kanbi-gh376-focus/pause-form-long-reason.txt`
- `/tmp/kanbi-gh376-focus/pause-form-after-up.txt`

These temporary evidence paths are specific to the investigation session. The
steps above are the durable reproduction recipe.

## Exploratory coverage and limits

The board agent exercised Master search, board/state filters, saving/applying
presets, board creation/archive/unarchive, ticket movement, help, and navigation
at 160×45 and 80×24. An additional 80×24 pass checked 28 boards, long board names,
rename/cwd inputs, and export layout. No additional bug was confirmed. With many
board options, the preset chooser was not fully verified onscreen; this remains
an investigation gap rather than a confirmed finding.

The Focus agent exercised settings, global overflow, two required checkpoint
pauses before admission, multiline input, and resizing between 80×24 and 160×45.
The editor agent inspected the picker, ticket inspector/body layout, template
board chooser, template list, and create form at 80×24. External-editor control
interrupted that pass; notes, file completion, and Unicode template saving remain
unverified. No other editor bug was confirmed.

All three owned Herdr fixtures were stopped. Their disposable databases remain
under `/tmp/kanbi-gh376-{boards,editor,focus}` for local investigation.

## Verification

- `go test -p 1 ./...`: passed. The initial parallel `go test ./...` failed during
  compilation/linking with disk quota errors; serialized package builds passed.
- `go vet ./...`: passed.
- `./scripts/smoke.sh --skip-checks`: passed, including fake-harness prompt/ref
  capture, focus, resume, and preserved session attempts in isolated Herdr.
- No production code changed, so no new regression test or formatting change was
  added.

## Follow-up fix

`updatePause` now reserves Tab/Shift+Tab for field navigation and sends Up/Down
to the textarea. A regression test exercises both directions in all three
multiline fields at widths 160 and 80, checks that text and focus are preserved,
and checks Tab/Shift+Tab navigation.

Follow-up verification passed: `go fmt ./...`, `go test ./...`, `go vet ./...`,
and `./scripts/smoke.sh --skip-checks`. The real UI was validated at 160×45 and
80×24 in `/tmp/kanbi-gh376-fix`: Up/Down followed by typing edited the first and
second lines respectively, Tab/Shift+Tab changed fields, and Ctrl+S saved the
complete checkpoint. The fixture database confirmed both edited lines and the
remaining fields were preserved. The owned Herdr fixture was stopped.
