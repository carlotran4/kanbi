package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/carlotran4/kanbi/internal/tui/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ticketWakeActions interface {
	TicketWakeups(context.Context, int64) ([]storage.TicketWakeup, error)
	ScheduleTicketPrompt(context.Context, int64, string, time.Time) error
	CancelTicketWake(context.Context, int64, int64) error
}
type ticketWakeUI struct {
	open, editing bool
	ticket        storage.Ticket
	items         []storage.TicketWakeup
	index, field  int
	at            InputBuffer
	prompt        textarea.Model
	error         string
}

func (m *Model) reloadTicketWakes() {
	a, ok := m.actions.(ticketWakeActions)
	if !ok {
		return
	}
	items, err := a.TicketWakeups(m.ctx, m.ticketWakes.ticket.ID)
	if err != nil {
		m.ticketWakes.error = err.Error()
		return
	}
	m.ticketWakes.items = items
	m.ticketWakes.index = minInt(m.ticketWakes.index, maxInt(0, len(items)-1))
}
func (m *Model) openTicketWakes(t storage.Ticket) {
	m.ticketWakes = ticketWakeUI{open: true, ticket: t}
	m.reloadTicketWakes()
}
func (m Model) updateTicketWakes(key tea.KeyMsg) (Model, tea.Cmd) {
	m.renderVersion++
	a, ok := m.actions.(ticketWakeActions)
	if !ok {
		return m, nil
	}
	m.resizeTicketWakeEditor()
	u := &m.ticketWakes
	if !u.editing {
		switch key.String() {
		case "esc":
			u.open = false
		case "r":
			u.error = ""
			m.reloadTicketWakes()
		case "j", "down":
			u.index = minInt(maxInt(0, len(u.items)-1), u.index+1)
		case "k", "up":
			u.index = maxInt(0, u.index-1)
		case "x":
			if len(u.items) > 0 {
				if err := a.CancelTicketWake(m.ctx, u.ticket.ID, u.items[u.index].ID); err != nil {
					u.error = err.Error()
				} else {
					u.error = ""
					m.reloadTicketWakes()
				}
			}
		case "n":
			u.editing = true
			u.field = 0
			u.error = ""
			u.at = NewInputBuffer(time.Now().AddDate(0, 0, 1).Format("2006-01-02 15:04"))
			u.prompt = newBodyTextarea("", m.width)
			u.prompt.CharLimit = 16384
			m.resizeTicketWakeEditor()
			u.prompt.Placeholder = "What should this ticket's agent do then?"
		}
		return m, nil
	}
	switch key.String() {
	case "esc":
		u.editing = false
		u.prompt.Blur()
		u.error = ""
	case "tab", "shift+tab":
		u.field = 1 - u.field
		if u.field == 1 {
			return m, u.prompt.Focus()
		}
		u.prompt.Blur()
	case "ctrl+s":
		due, err := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(u.at.Value()), time.Local)
		if err != nil {
			due, err = time.Parse(time.RFC3339, strings.TrimSpace(u.at.Value()))
		}
		if err != nil {
			u.error = "Use YYYY-MM-DD HH:MM (local time) or RFC3339"
			return m, nil
		}
		if err = a.ScheduleTicketPrompt(m.ctx, u.ticket.ID, u.prompt.Value(), due); err != nil {
			u.error = err.Error()
			return m, nil
		}
		u.editing = false
		u.prompt.Blur()
		u.error = ""
		m.reloadTicketWakes()
	default:
		if u.field == 0 {
			u.at.HandleKey(key.String(), key.Runes)
		} else {
			var cmd tea.Cmd
			u.prompt, cmd = u.prompt.Update(key)
			return m, cmd
		}
	}
	return m, nil
}
func (m Model) ticketWakesView() string {
	u := m.ticketWakes
	width := inspectorPopupWidth(m.width)
	inner := maxInt(1, width-6)
	muted := lipgloss.NewStyle().Foreground(palette.muted)
	lines := []string{trimToWidth(u.ticket.Title, inner), muted.Render("Runs while Kanbi is open; paused/archived tickets wait."), ""}
	footer := "n queue prompt · x cancel selected · r refresh · Esc close"
	if u.editing {
		lines = append(lines, modalLabeledInput("After: ", u.at, u.field == 0, inner), muted.Render("YYYY-MM-DD HH:MM · local "+time.Now().Format("MST -07:00")), "Prompt")
		u.prompt.SetWidth(inner)
		u.prompt.SetHeight(maxInt(2, minInt(12, m.height-14)))
		lines = append(lines, textareaOverlayView(u.prompt))
		footer = "Tab time/prompt · Ctrl+S queue · Esc back"
	} else {
		rows := maxInt(1, (m.height-13)/3)
		start := maxInt(0, u.index-rows+1)
		end := minInt(len(u.items), start+rows)
		if len(u.items) == 0 {
			lines = append(lines, "No scheduled prompts. Press n to queue one.")
		}
		for i := start; i < end; i++ {
			w := u.items[i]
			prefix := "  "
			if i == u.index {
				prefix = "▸ "
			}
			state := w.State
			if w.Delivery != "" {
				state += " / " + w.Delivery
			}
			lines = append(lines, trimToWidth(fmt.Sprintf("%s%s · %s", prefix, w.DueAt.Local().Format("Jan 2 2006 15:04"), state), inner))
			detail := strings.ReplaceAll(w.Prompt, "\n", " ")
			if w.Error != "" {
				detail = w.Error
			}
			if w.DeliveryError != "" {
				detail = w.DeliveryError
			}
			lines = append(lines, trimToWidth("  "+detail, inner), "")
		}
		if len(u.items) > 0 {
			lines = append(lines, muted.Render(fmt.Sprintf("%d/%d · ↑/↓ select", u.index+1, len(u.items))))
		}
	}
	if u.error != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(palette.error_).Render(trimToWidth(u.error, inner)))
	}
	lines = append(lines, muted.Render(trimToWidth(footer, inner)))
	return ticketInspectorBox(strings.Join(lines, "\n"), "Scheduled prompts · "+u.ticket.DisplayID, width)
}

func (m *Model) resizeTicketWakeEditor() {
	if !m.ticketWakes.editing {
		return
	}
	m.ticketWakes.prompt.SetWidth(maxInt(1, inspectorPopupWidth(m.width)-6))
	m.ticketWakes.prompt.SetHeight(maxInt(2, minInt(12, m.height-14)))
}
