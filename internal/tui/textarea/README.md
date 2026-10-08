# Compatible textarea performance patch

This package derives from `github.com/charmbracelet/bubbles/textarea` **v1.0.0**,
under the included Charmbracelet MIT license. The other Bubbles components,
including cursor, viewport, rune sanitization, and bounded wrapping memoization,
remain the pinned upstream dependencies.

Kanbi's changes are limited to:

- detecting an empty value without joining the whole document;
- keeping viewport row counts but skipping offscreen row styling when line
  numbers and dynamic prompts are disabled;
- calculating printable ASCII wrapping widths with rune counts, using exactly
  the original wrapping rules;
- retaining the original Unicode wrapping path for every other input.

This is not a new editor. The original keymap, cursor, viewport, clipboard,
character limits, Unicode handling, and text mutation operations remain intact.
`upstream_test.go` is the v1.0.0 textarea regression suite. Additional tests compare
complete state and ANSI views with the upstream component while editing,
resizing, pasting, scrolling, toggling focus, and using dynamic prompts. The ASCII
wrap path is checked against the original algorithm over 2,000 generated cases.

When upgrading Bubbles, compare this copy with that exact release, update the
upstream suite and license, and rerun the differential tests and real Kanbi UI
validation. Keep this patch small; do not silently change editor semantics.
