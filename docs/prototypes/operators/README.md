# Operator editor prototype

Design exploration only; application behavior is unchanged.

`operator-editor.html` is an interactive inline prototype. It uses the current
terminal palette (Tokyo Night in the captured environment; adaptive light preview)
and borrows the ticket editor's title border, metadata row, content emphasis and
keyboard footer. The instructions are the main content. Name occupies the title;
harness and working directory remain compact. A schedule summary opens a focused
schedule editor rather than leaving six configuration fields above the content.

The external-editor action is a local simulation. Save/cancel only change preview
feedback; no files, Operators or sessions are created. The host's design controls
compare Current/Proposed, creation/editing, and separate/inline scheduling. The
harness selector locks in editing mode.

## References and intentional departures

Reference `.txt` frames were captured from the real app in an isolated Herdr
workspace at 160×45 and 80×24. No production database was inspected. Ticket editing
uses a name in its border, a compact metadata line and a content-dominant body;
the current Operator form instead has a standalone heading and six labeled fields.
The Current comparison reproduces the captured, empty creation state. The proposed
state is populated with Intake to show realistic instruction wrapping and scheduling.

The prototype retains the board and its keyboard-first UI. It deliberately moves
schedule configuration into a separate local editor and adds the ticket editor's
Ctrl+E interaction to the proposed instructions flow. These are proposed changes,
not claims about implemented behavior. No Board/Operators tab hierarchy is added.

`proposed-*.txt` are exact terminal-cell sketches of both editors at 80×24 and
160×45. Regenerate with `python3 docs/prototypes/operators/render_wireframes.py`.
The inline preview is responsive; the text sketches establish exact TUI geometry.
At 80×24, the instructions viewport shows six text rows; at 160×45 it grows instead
of leaving the standing instructions permanently limited to six rows.

## Verification

Compared captured ticket and Operator frames at both terminal sizes. Generated
wireframes assert frame width and available height. Browser checks passed at 736px
and 320px: schedule/timezone editing, returning from the simulated external editor,
save feedback, and no horizontal overflow. Visually inspected the rendered desktop
preview. No production Go code was changed; Go tests and harness tests were not
rerun for this prototype.
