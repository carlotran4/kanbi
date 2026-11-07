package tui

import "github.com/carlotran4/kanbi/internal/tui/textarea"

// newTextEditor applies Kanbi's shared editing shortcuts without changing the
// vendored textarea defaults. Terminals commonly encode Ctrl+Backspace as BS
// (Ctrl+H), while ordinary Backspace sends DEL.
func newTextEditor() textarea.Model {
	ta := textarea.New()
	ta.KeyMap.DeleteWordBackward.SetKeys("ctrl+backspace", "ctrl+h", "alt+backspace", "ctrl+w")
	ta.KeyMap.DeleteCharacterBackward.SetKeys("backspace")
	return ta
}
