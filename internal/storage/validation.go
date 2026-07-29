package storage

import (
	"errors"
	"fmt"
	"strings"

	"github.com/carlotran4/kanbi/internal/harness"
)

type ticketWrite struct {
	Title   string
	Body    string
	Harness string
}

// normalizeTicketWrite normalizes fields shared by local ticket writes and
// imported aggregate tickets. Body is deliberately preserved byte-for-byte.
func normalizeTicketWrite(title, body, harnessName string, defaultBlankHarness bool) (ticketWrite, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return ticketWrite{}, errors.New("ticket title is required")
	}
	harnessName = strings.ToLower(strings.TrimSpace(harnessName))
	if harnessName == "" && defaultBlankHarness {
		harnessName = "pi"
	}
	if _, ok := harness.BuiltinContract(harnessName); !ok {
		return ticketWrite{}, fmt.Errorf("unsupported harness %q", harnessName)
	}
	return ticketWrite{Title: title, Body: body, Harness: harnessName}, nil
}

func normalizeBoardName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("board name is required")
	}
	return name, nil
}

func normalizeTicketBackend(backend string, defaultBlank bool) (string, error) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" && defaultBlank {
		backend = "local"
	}
	switch backend {
	case "local", "github", "atlassian":
		return backend, nil
	default:
		return "", fmt.Errorf("unsupported ticket backend %q", backend)
	}
}

func normalizeWorktreeMode(mode string, defaultBlank bool) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" && defaultBlank {
		mode = WorktreeModeOff
	}
	switch mode {
	case WorktreeModeOff, WorktreeModeGit:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported worktree mode %q", mode)
	}
}
