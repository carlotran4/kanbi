---
name: kanbi-wireframes
description: Create or revise realistic Kanbi TUI wireframes and visual redesign concepts. Use when proposing or showing Kanbi UI directions; ground the mockup in the running app, terminal dimensions, real content, and existing interactions.
---

# Kanbi wireframes

A wireframe should be a testable visual proposal for Kanbi as it exists. Make ambitious changes to appearance when asked, while preserving its information and keyboard behavior unless the user explicitly requests a UX change.

## Establish the baseline

- Inspect the real TUI before drawing it. Use the disposable Herdr fixture in [kanbi-ui-validation](../kanbi-ui-validation/SKILL.md) when available; never use the user's live database for exploratory mockups. Inspect the relevant screen, neighboring views, and a dense or awkward state. If the app cannot be run, inspect current code and screenshots and state consequential assumptions.
- Record the actual viewport, layout regions, content hierarchy, terminal palette, status vocabulary, and shortcuts. Treat the user's corrections about current behavior as ground truth.
- Use representative ticket content: plausible title lengths, counts, attention states, empty states, and overflow. Avoid polished placeholder data that conceals layout problems.

## Design the proposal

- Change the requested dimension. For a visual redesign, explore typography, density, surfaces, color, alignment, and hierarchy without silently adding navigation, controls, or new workflows. The result can look substantially different while retaining the same jobs and shortcuts.
- Honor terminal cell geometry, glyph rendering, and keyboard hints. Follow the active terminal theme by default; label a named palette such as Tokyo Night as a preview rather than implying Kanbi hard-codes it.
- Give each visible icon, badge, border, color, and label a purpose. Use Kanbi's actual status language and key labels. Do not add decorative glyphs, fake metrics, or unexplained controls.
- When showing alternatives, keep content, viewport, and behavior constant so the difference is visual. Include enough of the board and inspector to judge alignment, spacing, and fit.

## Check before showing

- Open or render the mockup at the intended terminal size and at 80×24. Check clipping, alignment, legibility, density, and long-content behavior. Exercise any tabs or toggles in the mockup.
- Compare the proposal with the observed TUI. Identify intentional behavior departures rather than passing them off as styling. A static image alone does not establish implementation fidelity.
- Show the visual and briefly explain its meaningful choices. Keep editable source so feedback can revise the same concept.

Follow the applicable visualization tool's output rules when displaying a mockup. This skill guides fidelity; it does not require a particular rendering tool.
