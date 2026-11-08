package tui

import (
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

// editTextExternally is shared by ticket descriptions and Operator text fields.
// $EDITOR is a user-configured command and may include flags or quoted arguments.
// The file path is passed as a separate shell argument.
// Drafts are written to a private temporary file and only applied on successful exit.
func editTextExternally(text string, done func(string, error) tea.Msg) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		if _, err := exec.LookPath("nano"); err == nil {
			editor = "nano"
		} else {
			editor = "vi"
		}
	}
	file, err := os.CreateTemp("", "kanbi-*.md")
	if err != nil {
		return func() tea.Msg { return done("", err) }
	}
	path := file.Name()
	_, writeErr := file.WriteString(text)
	closeErr := file.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(path)
		return func() tea.Msg { return done("", writeErr) }
	}
	return tea.ExecProcess(exec.Command("sh", "-c", "exec "+editor+` "$1"`, "kanbi-editor", path), func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return done("", err)
		}
		data, err := os.ReadFile(path)
		return done(string(data), err)
	})
}
