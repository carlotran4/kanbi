package tui

// RenderFPS bounds renderer cadence without changing inline mode, layout, or
// input repeat. Bubble Tea skips identical frames, so idle screens do not redraw.
const RenderFPS = 120
