package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"agent-kanban/internal/config"
	"agent-kanban/internal/storage"
	"agent-kanban/internal/ticketbackend"
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
		if tmux.InsideTmux() && os.Getenv("AGENT_KANBAN_TMUX_SESSION") == "" {
			if sessionName, err := tmux.CurrentSessionName(ctx); err == nil && sessionName != "" {
				cfg.TmuxSession = sessionName
				cfg.Tmux.SessionName = sessionName
			}
		}
		return runBoard(ctx, cfg)
	}

	switch args[0] {
	case "--board":
		return runBoard(ctx, cfg)
	case "doctor":
		return runDoctor(ctx, cfg)
	case "boards":
		return runBoards(ctx, cfg, args[1:])
	case "add":
		return runAdd(ctx, cfg, args[1:])
	case "list":
		return runList(ctx, cfg, args[1:])
	case "open":
		return runOpen(ctx, cfg, args[1:])
	default:
		return usageError(args[0])
	}
}

func runBoard(ctx context.Context, cfg config.Config) error {
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		manager := cli.Manager()
		_ = manager.Reconcile(ctx)
		syncer := ticketbackend.NewManager(cli.store)
		_ = syncer.SyncAll(ctx)
		stopSync := syncer.Start(ctx)
		defer stopSync()
		_, err := tea.NewProgram(tui.NewWithPicker(ctx, tui.NewService(cli.store, manager))).Run()
		return err
	})
}

func runDoctor(ctx context.Context, cfg config.Config) error {
	report := probeDoctor(ctx, cfg, defaultDoctorProber())
	printDoctorReport(os.Stdout, report)
	return report.FatalErr()
}

func runBoards(ctx context.Context, cfg config.Config, args []string) error {
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		if len(args) == 0 || args[0] == "list" {
			boards, err := cli.store.ListBoards(ctx)
			if err != nil {
				return err
			}
			fmt.Println("Master\t(all boards)")
			for _, b := range boards {
				fmt.Printf("%d\t%s\t%s\t%s\n", b.ID, b.Name, b.Workdir, b.TicketBackend)
			}
			return nil
		}
		if args[0] == "add" {
			opts, err := parseBoardAddArgs(args[1:])
			if err != nil {
				return err
			}
			if _, ok := ticketbackend.DefaultRegistry().Get(opts.TicketBackend); !ok {
				return fmt.Errorf("ticket backend %q is not implemented yet", opts.TicketBackend)
			}
			b, err := cli.store.CreateBoardWithOptions(ctx, opts)
			if err != nil {
				return err
			}
			fmt.Printf("%d\t%s\t%s\t%s\n", b.ID, b.Name, b.Workdir, b.TicketBackend)
			return nil
		}
		if args[0] == "rename" {
			if len(args) != 3 {
				return fmt.Errorf("usage: agent-kanban boards rename \"Old Name\" \"New Name\"")
			}
			b, err := cli.BoardByName(args[1])
			if err != nil {
				return err
			}
			if err := cli.store.RenameBoard(ctx, b.ID, args[2]); err != nil {
				return err
			}
			fmt.Printf("%d\t%s\n", b.ID, args[2])
			return nil
		}
		if args[0] == "set-cwd" {
			if len(args) != 3 {
				return fmt.Errorf("usage: agent-kanban boards set-cwd \"Name\" /path")
			}
			b, err := cli.BoardByName(args[1])
			if err != nil {
				return err
			}
			if err := cli.store.SetBoardWorkdir(ctx, b.ID, args[2]); err != nil {
				return err
			}
			updated, _ := cli.BoardByName(args[1])
			fmt.Printf("%d\t%s\t%s\t%s\n", updated.ID, updated.Name, updated.Workdir, updated.TicketBackend)
			return nil
		}
		return fmt.Errorf("usage: agent-kanban boards [list|add \"Name\" [--cwd /path] [--backend local|github|atlassian] [--query QUERY] [--config JSON]|rename OLD NEW|set-cwd NAME /path]")
	})
}

func runAdd(ctx context.Context, cfg config.Config, args []string) error {
	title, body, harnessName, boardName, err := parseAddArgs(args)
	if err != nil {
		return err
	}
	if title == "" {
		return fmt.Errorf("usage: agent-kanban add \"title\" --body \"...\" --harness pi")
	}
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		board, err := cli.ResolveBoardView(boardName)
		if err != nil {
			return err
		}
		if len(board.Columns) == 0 {
			return fmt.Errorf("board %q has no columns", board.Board.Name)
		}
		t, err := cli.store.CreateTicket(ctx, board.Columns[0].ID, title, body, harnessName)
		if err != nil {
			return err
		}
		fmt.Printf("%s %s [%s]\n", t.DisplayID, t.Title, t.Harness)
		return nil
	})
}

func runList(ctx context.Context, cfg config.Config, args []string) error {
	boardName, err := parseOptionalBoard(args)
	if err != nil {
		return err
	}
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		tickets, err := cli.ResolveTickets(boardName)
		if err != nil {
			return err
		}
		for _, t := range tickets {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\n", t.BoardName, t.DisplayID, t.Harness, t.Runtime, t.Title)
		}
		return nil
	})
}

func runOpen(ctx context.Context, cfg config.Config, args []string) error {
	displayID, sendPrompt, boardName, err := parseOpenArgs(args)
	if err != nil {
		return err
	}
	if displayID == "" {
		return fmt.Errorf("usage: agent-kanban open T-001 [--board NAME] [--send-prompt]")
	}
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		ticket, err := cli.ResolveTicket(displayID, boardName)
		if err != nil {
			return err
		}
		if err := cli.Manager().OpenTicket(ctx, ticket, sendPrompt); err != nil {
			return err
		}
		fmt.Println("opened", ticket.BoardName, ticket.DisplayID, tmux.TicketWindowName(ticket))
		return nil
	})
}

type cliContext struct {
	ctx   context.Context
	cfg   config.Config
	store *storage.Store
}

func newCLIContext(ctx context.Context, cfg config.Config) (*cliContext, error) {
	store, err := openStore(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &cliContext{ctx: ctx, cfg: cfg, store: store}, nil
}

func withCLIContext(ctx context.Context, cfg config.Config, fn func(*cliContext) error) error {
	cli, err := newCLIContext(ctx, cfg)
	if err != nil {
		return err
	}
	defer cli.Close()
	return fn(cli)
}

func (c *cliContext) Close() error {
	return c.store.Close()
}

func (c *cliContext) Manager() *tmux.Manager {
	return tmux.NewManager(c.cfg, c.store)
}

func (c *cliContext) BoardByName(name string) (storage.Board, error) {
	return c.store.BoardByName(c.ctx, name)
}

func (c *cliContext) ResolveBoardView(boardName string) (storage.BoardView, error) {
	if boardName == "" {
		return c.store.BoardView(c.ctx)
	}
	board, err := c.BoardByName(boardName)
	if err != nil {
		return storage.BoardView{}, err
	}
	return c.store.BoardViewByID(c.ctx, board.ID)
}

func (c *cliContext) ResolveTickets(boardName string) ([]storage.Ticket, error) {
	if boardName == "" {
		return c.store.ListTickets(c.ctx, false)
	}
	view, err := c.ResolveBoardView(boardName)
	if err != nil {
		return nil, err
	}
	var tickets []storage.Ticket
	for _, col := range view.Columns {
		tickets = append(tickets, col.Tickets...)
	}
	return tickets, nil
}

func (c *cliContext) ResolveTicket(displayID, boardName string) (storage.Ticket, error) {
	displayID = strings.ToUpper(displayID)
	if boardName == "" {
		return c.store.TicketByDisplayID(c.ctx, displayID)
	}
	board, err := c.BoardByName(boardName)
	if err != nil {
		return storage.Ticket{}, err
	}
	return c.store.TicketByDisplayIDInBoard(c.ctx, displayID, board.ID)
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
	return fmt.Errorf("unknown command %q\nusage: agent-kanban [doctor|boards|add|list|open|--board]", cmd)
}

func parseBoardAddArgs(args []string) (storage.CreateBoardOptions, error) {
	opts := storage.CreateBoardOptions{TicketBackend: ticketbackend.KindLocal}
	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		opts.Workdir = cwd
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--cwd":
			i++
			if i >= len(args) {
				return storage.CreateBoardOptions{}, fmt.Errorf("--cwd requires a value")
			}
			opts.Workdir = args[i]
		case "--backend":
			i++
			if i >= len(args) {
				return storage.CreateBoardOptions{}, fmt.Errorf("--backend requires a value")
			}
			opts.TicketBackend = strings.TrimSpace(args[i])
		case "--query":
			i++
			if i >= len(args) {
				return storage.CreateBoardOptions{}, fmt.Errorf("--query requires a value")
			}
			opts.BackendQuery = args[i]
		case "--config":
			i++
			if i >= len(args) {
				return storage.CreateBoardOptions{}, fmt.Errorf("--config requires a JSON value")
			}
			opts.BackendConfig = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return storage.CreateBoardOptions{}, fmt.Errorf("unknown boards add flag %s", args[i])
			}
			if opts.Name != "" {
				return storage.CreateBoardOptions{}, fmt.Errorf("boards add accepts one name")
			}
			opts.Name = args[i]
		}
	}
	if opts.Name == "" {
		return storage.CreateBoardOptions{}, fmt.Errorf("usage: agent-kanban boards add \"Name\" [--cwd /path] [--backend local|github|atlassian] [--query QUERY] [--config JSON]")
	}
	if opts.TicketBackend == "" {
		opts.TicketBackend = ticketbackend.KindLocal
	}
	return opts, nil
}

func parseAddArgs(args []string) (title, body, harnessName, boardName string, err error) {
	harnessName = "pi"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--body":
			i++
			if i >= len(args) {
				return "", "", "", "", fmt.Errorf("--body requires a value")
			}
			body = args[i]
		case "--harness":
			i++
			if i >= len(args) {
				return "", "", "", "", fmt.Errorf("--harness requires a value")
			}
			harnessName = args[i]
		case "--board":
			i++
			if i >= len(args) {
				return "", "", "", "", fmt.Errorf("--board requires a value")
			}
			boardName = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", "", "", fmt.Errorf("unknown add flag %s", args[i])
			}
			if title != "" {
				return "", "", "", "", fmt.Errorf("add accepts one title")
			}
			title = args[i]
		}
	}
	return title, body, harnessName, boardName, nil
}

func parseOptionalBoard(args []string) (string, error) {
	boardName := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--board":
			i++
			if i >= len(args) {
				return "", fmt.Errorf("--board requires a value")
			}
			boardName = args[i]
		default:
			return "", fmt.Errorf("unknown list flag %s", args[i])
		}
	}
	return boardName, nil
}

func parseOpenArgs(args []string) (displayID string, sendPrompt bool, boardName string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--send-prompt":
			sendPrompt = true
		case "--board":
			i++
			if i >= len(args) {
				return "", false, "", fmt.Errorf("--board requires a value")
			}
			boardName = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", false, "", fmt.Errorf("unknown open flag %s", args[i])
			}
			if displayID != "" {
				return "", false, "", fmt.Errorf("open accepts one display id")
			}
			displayID = args[i]
		}
	}
	return displayID, sendPrompt, boardName, nil
}
