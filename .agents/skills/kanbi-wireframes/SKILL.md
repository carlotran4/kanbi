---
name: kanbi-wireframes
description: Make faithful Kanbi TUI wireframes and mockups. Use when drawing an existing or proposed Kanbi screen; first match the real app's structure, content density, terminal size, and state before changing its visual treatment.
---

# Kanbi wireframes

A credible Kanbi wireframe starts as a faithful reconstruction of one real screen. Establish that baseline before exploring a visual direction. This skill is about accuracy of the mockup, not how to implement a redesign.

## Capture the screen

- Inspect the actual TUI at the intended terminal size. The disposable Herdr fixture in [kanbi-ui-validation](../kanbi-ui-validation/SKILL.md) can provide real board and inspector frames without touching the user's live database. If it cannot run, use current screenshots and code, and disclose any important assumption.
- Capture one concrete state before drawing: selected board and ticket, visible columns and counts, focus and attention states, inspector tab if open, terminal theme, and viewport dimensions. Check a crowded or long-content state when it affects the requested screen.
- Treat the observed app and the user's corrections as the source of truth. Existing behavior such as inspector/preview exclusivity, title emphasis, shortcuts, and terminal-controlled color must not be reimagined as generic dashboard features.

## Reconstruct the baseline

- Match the screen's topology first: header, column order and widths, card grouping, selected state, inspector placement, footer, and where space is intentionally empty. Preserve realistic card density and wrapping at the captured viewport.
- Use the captured labels, key casing, status vocabulary, and meaningful glyphs. If content must be substituted, retain the same lengths, counts, and awkward cases. Do not invent extra buttons, metrics, panels, decorative icons, or tidy placeholder tickets to fill space.
- Respect terminal cell geometry and glyph width. Use the active terminal palette by default; a named theme is a labeled preview, not a product color change.

## Compare before showing

- Render the baseline at the captured size and compare it with the real frame. Check proportions, alignment, visible item count, wrapping, focus cues, labels, and footer placement. Also inspect 80×24 when the proposed layout needs to work there.
- Correct mismatches before making style variants. For variants, keep the same content, state, viewport, and interactions so the visual change is the only variable. Clearly identify any deliberate departure from the baseline.
- Show the inspectable mockup and keep its editable source for revisions. A polished image is not evidence that its underlying screen is accurate.

Follow the applicable visualization tool's output rules. This skill does not require a particular rendering tool.
