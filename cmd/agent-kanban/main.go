package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"agent-kanban/internal/config"
	"agent-kanban/internal/storage"
	"agent-kanban/internal/tmux"
	"agent-kanban/internal/tui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agent-kanban:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	if len(args) == 0 {
		if !tmux.InsideTmux() && os.Getenv("AGENT_KANBAN_INNER") == "" {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			return tmux.AttachCommand(cfg, exe).Run()
		}
		return runBoard(ctx, cfg)
	}

	switch args[0] {
	case "--board":
		return runBoard(ctx, cfg)
	case "doctor":
		return runDoctor(ctx, cfg)
	case "add":
		return runAdd(ctx, cfg, args[1:])
	case "list":
		return runList(ctx, cfg)
	case "open":
		return runOpen(ctx, cfg, args[1:])
	default:
		return usageError(args[0])
	}
}

func runBoard(ctx context.Context, cfg config.Config) error {
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	manager := tmux.NewManager(cfg, store)
	_ = manager.Reconcile(ctx)
	_, err = tea.NewProgram(tui.New(ctx, boardService{store: store, manager: manager})).Run()
	return err
}

func runDoctor(ctx context.Context, cfg config.Config) error {
	fmt.Println("agent-kanban doctor")
	if path, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux is required: %w", err)
	} else if out, err := exec.Command(path, "-V").CombinedOutput(); err == nil {
		fmt.Println("ok tmux", strings.TrimSpace(string(out)))
	} else {
		fmt.Println("ok tmux")
	}
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	fmt.Println("ok sqlite", cfg.DBPath)
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}
	fmt.Println("ok config", cfg.Paths.ConfigFile)
	if shell := os.Getenv("SHELL"); shell != "" {
		fmt.Println("ok shell", shell)
	} else {
		fmt.Println("warn shell not detected")
	}
	if tmux.InsideTmux() {
		fmt.Println("ok inside tmux")
	} else {
		fmt.Println("warn not inside tmux")
	}
	if os.Getenv("TERM") != "" {
		fmt.Println("ok terminal", os.Getenv("TERM"))
	} else {
		fmt.Println("warn terminal unknown")
	}
	manager := tmux.NewManager(cfg, store)
	if err := manager.EnsureSession(ctx); err != nil {
		return fmt.Errorf("tmux session unusable: %w", err)
	}
	fmt.Println("ok tmux session", cfg.TmuxSession)
	for name, h := range cfg.Harnesses {
		if len(h.Start) == 0 {
			fmt.Println("warn harness", name, "has no start command")
			continue
		}
		if _, err := exec.LookPath(h.Start[0]); err != nil {
			fmt.Println("warn harness", name, "not found:", h.Start[0])
		} else {
			fmt.Println("ok harness", name)
		}
	}
	return nil
}

func runAdd(ctx context.Context, cfg config.Config, args []string) error {
	title, body, harnessName, err := parseAddArgs(args)
	if err != nil {
		return err
	}
	if title == "" {
		return fmt.Errorf("usage: agent-kanban add \"title\" --body \"...\" --harness pi")
	}
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	board, err := store.BoardView(ctx)
	if err != nil {
		return err
	}
	if len(board.Columns) == 0 {
		return fmt.Errorf("default board has no columns")
	}
	t, err := store.CreateTicket(ctx, board.Columns[0].ID, title, body, harnessName)
	if err != nil {
		return err
	}
	fmt.Printf("%s %s [%s]\n", t.DisplayID, t.Title, t.Harness)
	return nil
}

func runList(ctx context.Context, cfg config.Config) error {
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	tickets, err := store.ListTickets(ctx, false)
	if err != nil {
		return err
	}
	for _, t := range tickets {
		fmt.Printf("%s\t%s\t%s\t%s\n", t.DisplayID, t.Harness, t.Runtime, t.Title)
	}
	return nil
}

func runOpen(ctx context.Context, cfg config.Config, args []string) error {
	displayID, sendPrompt, err := parseOpenArgs(args)
	if err != nil {
		return err
	}
	if displayID == "" {
		return fmt.Errorf("usage: agent-kanban open T-001 [--send-prompt]")
	}
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	ticket, err := store.TicketByDisplayID(ctx, strings.ToUpper(displayID))
	if err != nil {
		return err
	}
	manager := tmux.NewManager(cfg, store)
	if err := manager.OpenTicket(ctx, ticket, sendPrompt); err != nil {
		return err
	}
	fmt.Println("opened", ticket.DisplayID, tmux.WindowName(ticket.DisplayID, ticket.Title))
	return nil
}

func openStore(ctx context.Context, cfg config.Config) (*storage.Store, error) {
	store, err := storage.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := store.Init(ctx); err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}

func usageError(cmd string) error {
	return fmt.Errorf("unknown command %q\nusage: agent-kanban [doctor|add|list|open|--board]", cmd)
}

type boardService struct {
	store   *storage.Store
	manager *tmux.Manager
}

func (b boardService) BoardView(ctx context.Context) (storage.BoardView, error) {
	return b.store.BoardView(ctx)
}

func (b boardService) CreateTicket(ctx context.Context, columnID int64, title, body, harnessName string) (storage.Ticket, error) {
	return b.store.CreateTicket(ctx, columnID, title, body, harnessName)
}

func (b boardService) UpdateTicket(ctx context.Context, id int64, title, body, harnessName string) error {
	before, err := b.store.TicketByID(ctx, id)
	if err != nil {
		return err
	}
	if before.Title != title && before.WindowName.Valid {
		if err := b.manager.RenameTicketWindow(ctx, before, title); err != nil {
			return err
		}
	}
	return b.store.UpdateTicket(ctx, id, title, body, harnessName)
}

func (b boardService) OpenTicket(ctx context.Context, ticket storage.Ticket, sendPrompt bool) error {
	if sendPrompt {
		return b.manager.OpenTicket(ctx, ticket, true)
	}
	return b.manager.SwitchToTicket(ctx, ticket)
}

func (b boardService) ArchiveTicket(ctx context.Context, id int64) error {
	return b.store.ArchiveTicket(ctx, id)
}

func (b boardService) MoveTicket(ctx context.Context, id, columnID int64) error {
	return b.store.MoveTicket(ctx, id, columnID)
}

func (b boardService) ReorderTicket(ctx context.Context, id int64, delta int) error {
	return b.store.ReorderTicket(ctx, id, delta)
}

func (b boardService) RefreshRuntime(ctx context.Context) error {
	return b.manager.RefreshRuntime(ctx)
}

func (b boardService) MarkTicketState(ctx context.Context, ticketID int64, state string) error {
	return b.store.MarkTicketRuntime(ctx, ticketID, state, "manual", "manual override")
}

func (b boardService) AddColumn(ctx context.Context, boardID int64, name string) (storage.Column, error) {
	return b.store.AddColumn(ctx, boardID, name)
}

func (b boardService) RenameColumn(ctx context.Context, columnID int64, name string) error {
	return b.store.RenameColumn(ctx, columnID, name)
}

func (b boardService) DeleteColumn(ctx context.Context, columnID int64) error {
	return b.store.DeleteColumn(ctx, columnID)
}

func (b boardService) ReorderColumn(ctx context.Context, columnID int64, delta int) error {
	return b.store.ReorderColumn(ctx, columnID, delta)
}

func (b boardService) PastePromptNow(ctx context.Context, windowName, text string) error {
	return b.manager.PastePromptNow(ctx, windowName, text)
}

func (b boardService) CloseTicketSession(ctx context.Context, ticket storage.Ticket) error {
	return b.manager.CloseSession(ctx, ticket)
}

func (b boardService) KillAllSessions(ctx context.Context) error {
	return b.manager.KillSession(ctx)
}

func (b boardService) StartFreshTicket(ctx context.Context, ticket storage.Ticket, sendPrompt bool) error {
	return b.manager.StartFreshTicket(ctx, ticket, sendPrompt)
}

func (b boardService) UpdateSessionRef(ctx context.Context, ticket storage.Ticket, ref string) error {
	if ticket.SessionID.Valid {
		return b.store.UpdateSessionRef(ctx, ticket.SessionID.Int64, ref)
	}
	return fmt.Errorf("ticket has no session to repair")
}

func parseAddArgs(args []string) (title, body, harnessName string, err error) {
	harnessName = "pi"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--body":
			i++
			if i >= len(args) {
				return "", "", "", fmt.Errorf("--body requires a value")
			}
			body = args[i]
		case "--harness":
			i++
			if i >= len(args) {
				return "", "", "", fmt.Errorf("--harness requires a value")
			}
			harnessName = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", "", fmt.Errorf("unknown add flag %s", args[i])
			}
			if title != "" {
				return "", "", "", fmt.Errorf("add accepts one title")
			}
			title = args[i]
		}
	}
	return title, body, harnessName, nil
}

func parseOpenArgs(args []string) (displayID string, sendPrompt bool, err error) {
	for _, arg := range args {
		switch arg {
		case "--send-prompt":
			sendPrompt = true
		default:
			if strings.HasPrefix(arg, "-") {
				return "", false, fmt.Errorf("unknown open flag %s", arg)
			}
			if displayID != "" {
				return "", false, fmt.Errorf("open accepts one display id")
			}
			displayID = arg
		}
	}
	return displayID, sendPrompt, nil
}
