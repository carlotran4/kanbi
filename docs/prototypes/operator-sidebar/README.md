# Operator sidebar alternatives

These retain the design comparison that led to the approved **A · Board cards**
implementation. B and C remain alternatives; Current is the historical baseline.
The Operator editor was implemented separately in `aa6fc80`.

Open `operator-sidebars.html` to compare:

- **A · Board cards:** the board's filled surface, left selection/attention rail,
  separate name/harness row, state and next wake. Closest to the existing cards.
- **B · Compact roster:** two-line entries, restrained selection and explicit
  attention labels. More Operators fit without using card surfaces everywhere.
- **C · List + details:** a short list with a dedicated selected-Operator summary
  above the footer. Trades some at-a-glance detail for a quieter list.
- **Current:** the unmodified real sidebar capture for comparison.

All alternatives keep the board, terminal palette, columns and selected ticket.
The panel occupies the same 36-column area in the wide layouts. A single vertical
rule separates it from the board, the heading follows the column headings, and
controls stay at the bottom rather than following the last list entry. The full
action list is behind `? actions`; its existing keyboard shortcuts remain visible
there. No nested board tabs or separate inspector are introduced.

At 80×24, the proposed focused list is a 40-column overlay on the right, preserving
some board context. This deliberately replaces the current near-full-width overlay.
The list shows the rows that fit, follows keyboard selection and indicates hidden
entries. A smaller browser shows the sidebar detail alone without reducing the
terminal font below a readable size. Automatic wide previews use 120×36 or 160×45
as space permits.

## Sources and controls

`current-*.ansi` and `.text` were captured in the project UI skill's isolated Herdr
fixture at 160×45, 120×36 and 80×24. The fixture has six boardless Operators using
the four supported harness identities, one intentionally paused schedule, and one
seeded resume error. No authenticated sessions were started, and no production
database was inspected. `board-80x24.*` records the board after returning focus.

The baseline is reconstructed directly from the ANSI frames, including the actual
card fills, selected ticket, column counts, board wrapping, focus labels and footer.
ANSI colors map to the captured terminal palette, with a light appearance preview.
Browser font fallback can render terminal glyphs differently from Herdr.

The default content and initial selection match the captured fixture for all three
alternatives. **Busy + attention** is a clearly labeled hypothetical state preview;
**Crowded list** adds six entries to test overflow. Current always shows the actual
capture. Clicking rows or using Up/Down / j/k selects an Operator. Run, pause/resume,
actions and focus feedback are local simulations; no Kanbi commands are invoked.
Open/edit/new only announce which action would occur. Collapse hides the panel
contents locally; reclaiming board space is not modeled in this prototype.

Host design controls select the concept, content and terminal preview. The same
choices appear as native selectors when opened without the host controls.

`sidebar.template.html` is editable source. Regenerate the self-contained comparison
with `python3 docs/prototypes/operator-sidebar/build.py`.

## Verification

Real reference captures cover 160×45, 120×36 and 80×24. Inspected browser renders
against the references and corrected terminal background fill gaps. Automated
browser checks passed at 1280, 1024, 736 and 320px: every concept and the baseline,
no horizontal overflow or clipped list rows, keyboard navigation to the final
crowded entry, attention examples, schedule pause, Run now and collapse/reopen.
The app's editor change passed fmt, the full Go suite, vet, smoke, and real Herdr
editing checks at 160×45 and 80×24 before this design-only commit.
