package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kanbi/internal/boardruntime"
	"kanbi/internal/config"
	"kanbi/internal/storage"
	"kanbi/internal/ticketbackend"
	"kanbi/internal/tmux"
	"kanbi/internal/tui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "kanbi:", err)
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
		if shouldLaunchHerdrBoard(cfg) && os.Getenv("KANBI_INNER") == "" {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			return boardruntime.LaunchHerdrBoard(cfg, exe)
		}
		if shouldAttachTmuxForBoard(cfg) && !tmux.InsideTmux() && os.Getenv("KANBI_INNER") == "" {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			return tmux.AttachCommand(cfg, exe).Run()
		}
		if tmux.InsideTmux() && os.Getenv("KANBI_TMUX_SESSION") == "" {
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
		return runDoctor(ctx, cfg, args[1:]...)
	case "boards":
		return runBoards(ctx, cfg, args[1:])
	case "add":
		return runAdd(ctx, cfg, args[1:])
	case "list":
		return runList(ctx, cfg, args[1:])
	case "show", "get":
		return runShow(ctx, cfg, args[1:])
	case "update":
		return runUpdate(ctx, cfg, args[1:])
	case "move":
		return runMove(ctx, cfg, args[1:])
	case "notes":
		return runNotes(ctx, cfg, args[1:])
	case "state":
		return runState(ctx, cfg, args[1:])
	case "sync":
		return runSync(ctx, cfg, args[1:])
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
		stopSync := syncer.Start(ctx)
		defer stopSync()
		_, err := tea.NewProgram(tui.NewWithPicker(ctx, tui.NewServiceWithSyncer(cli.store, manager, syncer))).Run()
		return err
	})
}

func runDoctor(ctx context.Context, cfg config.Config, args ...string) error {
	format, cleaned, err := parseFormat(args)
	if err != nil {
		return err
	}
	if len(cleaned) != 0 {
		return fmt.Errorf("usage: kanbi doctor [--json]")
	}
	report := probeDoctor(ctx, cfg, defaultDoctorProber())
	if format.JSON {
		results := make([]any, 0, len(report.Results))
		for _, r := range report.Results {
			errText := ""
			if r.Err != nil {
				errText = r.Err.Error()
			}
			results = append(results, map[string]any{"severity": r.Severity, "name": r.Name, "detail": r.Detail, "error": errText})
		}
		if err := writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.doctor", "results": results}); err != nil {
			return err
		}
	} else {
		printDoctorReport(os.Stdout, report)
	}
	return report.FatalErr()
}

func runBoards(ctx context.Context, cfg config.Config, args []string) error {
	format, args, err := parseFormat(args)
	if err != nil {
		return err
	}
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		if len(args) == 0 || args[0] == "list" {
			boards, err := cli.store.ListBoards(ctx)
			if err != nil {
				return err
			}
			if format.JSON {
				return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.boards", "boards": jsonBoards(boards)})
			}
			fmt.Println("Master\t(all boards)")
			for _, b := range boards {
				status := "ok"
				if b.LastSyncError.Valid && strings.TrimSpace(b.LastSyncError.String) != "" {
					status = "sync_error=" + b.LastSyncError.String
				}
				fmt.Printf("%d\t%s\t%s\t%s\t%s\n", b.ID, b.Name, b.Workdir, b.TicketBackend, status)
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
			if format.JSON {
				return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.board", "board": jsonBoard(b)})
			}
			fmt.Printf("%d\t%s\t%s\t%s\n", b.ID, b.Name, b.Workdir, b.TicketBackend)
			return nil
		}
		if args[0] == "rename" {
			if len(args) != 3 {
				return fmt.Errorf("usage: kanbi boards rename \"Old Name\" \"New Name\"")
			}
			b, err := cli.BoardByName(args[1])
			if err != nil {
				return err
			}
			if err := cli.store.RenameBoard(ctx, b.ID, args[2]); err != nil {
				return err
			}
			b.Name = args[2]
			if format.JSON {
				return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.board", "board": jsonBoard(b)})
			}
			fmt.Printf("%d\t%s\n", b.ID, args[2])
			return nil
		}
		if args[0] == "set-cwd" {
			if len(args) != 3 {
				return fmt.Errorf("usage: kanbi boards set-cwd \"Name\" /path")
			}
			b, err := cli.BoardByName(args[1])
			if err != nil {
				return err
			}
			if err := cli.store.SetBoardWorkdir(ctx, b.ID, args[2]); err != nil {
				return err
			}
			updated, _ := cli.BoardByName(args[1])
			if format.JSON {
				return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.board", "board": jsonBoard(updated)})
			}
			fmt.Printf("%d\t%s\t%s\t%s\n", updated.ID, updated.Name, updated.Workdir, updated.TicketBackend)
			return nil
		}
		return fmt.Errorf("usage: kanbi boards [list|add \"Name\" [--cwd /path] [--backend local|github|atlassian] [--query QUERY] [--config JSON]|rename OLD NEW|set-cwd NAME /path]")
	})
}

func runAdd(ctx context.Context, cfg config.Config, args []string) error {
	format, cleaned, err := parseFormat(args)
	if err != nil {
		return err
	}
	title, body, harnessName, boardName, err := parseAddArgs(cleaned)
	if err != nil {
		return err
	}
	if title == "" {
		return fmt.Errorf("usage: kanbi add \"title\" --body \"...\" --harness pi")
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
		if format.JSON {
			ticket, err := cli.store.TicketByID(ctx, t.ID)
			if err != nil {
				return err
			}
			return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.ticket", "ticket": cli.jsonTicket(ctx, ticket, true)})
		}
		fmt.Printf("%s %s [%s]\n", t.DisplayID, t.Title, t.Harness)
		return nil
	})
}

func runList(ctx context.Context, cfg config.Config, args []string) error {
	format, cleaned, err := parseFormat(args)
	if err != nil {
		return err
	}
	boardName, err := parseOptionalBoard(cleaned)
	if err != nil {
		return err
	}
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		tickets, err := cli.ResolveTickets(boardName)
		if err != nil {
			return err
		}
		if format.JSON {
			out := make([]any, 0, len(tickets))
			for _, t := range tickets {
				out = append(out, cli.jsonTicket(ctx, t, false))
			}
			return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.tickets", "tickets": out})
		}
		for _, t := range tickets {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\n", t.BoardName, t.DisplayID, t.Harness, t.Runtime, t.Title)
		}
		return nil
	})
}

func runSync(ctx context.Context, cfg config.Config, args []string) error {
	format, cleaned, err := parseFormat(args)
	if err != nil {
		return err
	}
	boardName, err := parseOptionalBoard(cleaned)
	if err != nil {
		return err
	}
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		syncer := ticketbackend.NewManager(cli.store)
		var boards []storage.Board
		if boardName == "" {
			boards, err = cli.store.ListBoards(ctx)
			if err != nil {
				return err
			}
		} else {
			board, err := cli.BoardByName(boardName)
			if err != nil {
				return err
			}
			boards = []storage.Board{board}
		}
		var syncErrs []error
		var results []any
		for _, board := range boards {
			res, err := syncer.SyncBoard(ctx, board)
			if err != nil {
				if format.JSON {
					results = append(results, map[string]any{"board": jsonBoard(board), "status": "sync_error", "error": err.Error()})
				} else {
					fmt.Printf("sync-error\t%s\t%s\tlast_sync_error=%s\n", board.Name, board.TicketBackend, err)
				}
				syncErrs = append(syncErrs, fmt.Errorf("%s: %w", board.Name, err))
				continue
			}
			if format.JSON {
				results = append(results, map[string]any{"board": jsonBoard(board), "status": "synced", "pulled": res.Pulled, "pushed": res.Pushed, "conflicts": res.Conflicts})
			} else {
				fmt.Printf("synced\t%s\t%s\tpulled=%d\tpushed=%d\tconflicts=%d\n", board.Name, board.TicketBackend, res.Pulled, res.Pushed, res.Conflicts)
			}
		}
		if format.JSON {
			if err := writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.sync", "results": results}); err != nil {
				return err
			}
		}
		return errors.Join(syncErrs...)
	})
}

func runOpen(ctx context.Context, cfg config.Config, args []string) error {
	format, cleaned, err := parseFormat(args)
	if err != nil {
		return err
	}
	displayID, sendPrompt, boardName, err := parseOpenArgs(cleaned)
	if err != nil {
		return err
	}
	if displayID == "" {
		return fmt.Errorf("usage: kanbi open T-001 [--board NAME] [--send-prompt]")
	}
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		ticket, err := cli.ResolveTicket(displayID, boardName)
		if err != nil {
			return err
		}
		if err := cli.Manager().OpenTicket(ctx, ticket, sendPrompt); err != nil {
			return err
		}
		if format.JSON {
			updated, err := cli.store.TicketByID(ctx, ticket.ID)
			if err != nil {
				return err
			}
			return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.open", "action": "opened", "ticket": cli.jsonTicket(ctx, updated, false), "container_name": tmux.TicketWindowName(ticket), "tmux_window_name": tmux.TicketWindowName(ticket)})
		}
		fmt.Println("opened", ticket.BoardName, ticket.DisplayID, tmux.TicketWindowName(ticket))
		return nil
	})
}

func runShow(ctx context.Context, cfg config.Config, args []string) error {
	format, cleaned, err := parseFormat(args)
	if err != nil {
		return err
	}
	displayID, boardName, err := parseTicketRefArgs(cleaned)
	if err != nil {
		return err
	}
	if displayID == "" {
		return fmt.Errorf("usage: kanbi show T-001 [--board NAME] [--json]")
	}
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		ticket, err := cli.ResolveTicket(displayID, boardName)
		if err != nil {
			return err
		}
		if format.JSON {
			return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.ticket", "ticket": cli.jsonTicket(ctx, ticket, true)})
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n\n%s\n", ticket.BoardName, ticket.DisplayID, ticket.Harness, ticket.Runtime, ticket.Title, ticket.Body)
		return nil
	})
}

func runUpdate(ctx context.Context, cfg config.Config, args []string) error {
	format, cleaned, err := parseFormat(args)
	if err != nil {
		return err
	}
	opts, err := parseUpdateArgs(cleaned)
	if err != nil {
		return err
	}
	if opts.DisplayID == "" {
		return fmt.Errorf("usage: kanbi update T-001 [--board NAME] [--title TITLE] [--body BODY|--body-file PATH|--body-stdin] [--harness NAME] [--json]")
	}
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		ticket, err := cli.ResolveTicket(opts.DisplayID, opts.BoardName)
		if err != nil {
			return err
		}
		title, body, harnessName := ticket.Title, ticket.Body, ticket.Harness
		if opts.Title != nil {
			title = *opts.Title
		}
		if opts.Body != nil {
			body = *opts.Body
		}
		if opts.Harness != nil {
			harnessName = *opts.Harness
		}
		if err := cli.store.UpdateTicket(ctx, ticket.ID, title, body, harnessName); err != nil {
			return err
		}
		updated, err := cli.store.TicketByID(ctx, ticket.ID)
		if err != nil {
			return err
		}
		if format.JSON {
			return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.ticket", "ticket": cli.jsonTicket(ctx, updated, true)})
		}
		fmt.Printf("%s %s [%s]\n", updated.DisplayID, updated.Title, updated.Harness)
		return nil
	})
}

func runMove(ctx context.Context, cfg config.Config, args []string) error {
	format, cleaned, err := parseFormat(args)
	if err != nil {
		return err
	}
	displayID, boardName, toColumn, err := parseMoveArgs(cleaned)
	if err != nil {
		return err
	}
	if displayID == "" || toColumn == "" {
		return fmt.Errorf("usage: kanbi move T-001 --to COLUMN [--board NAME] [--json]")
	}
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		ticket, err := cli.ResolveTicket(displayID, boardName)
		if err != nil {
			return err
		}
		colID, err := cli.store.ColumnIDByBoardAndName(ctx, ticket.BoardID, toColumn)
		if err != nil {
			return err
		}
		if err := cli.store.MoveTicket(ctx, ticket.ID, colID); err != nil {
			return err
		}
		updated, err := cli.store.TicketByID(ctx, ticket.ID)
		if err != nil {
			return err
		}
		if format.JSON {
			return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.ticket", "ticket": cli.jsonTicket(ctx, updated, true)})
		}
		fmt.Printf("%s moved to %s\n", updated.DisplayID, toColumn)
		return nil
	})
}

func runNotes(ctx context.Context, cfg config.Config, args []string) error {
	format, cleaned, err := parseFormat(args)
	if err != nil {
		return err
	}
	if len(cleaned) == 0 {
		return fmt.Errorf("usage: kanbi notes [list|add] T-001 [--board NAME] [--body BODY|--body-file PATH|--body-stdin] [--json]")
	}
	action := cleaned[0]
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		switch action {
		case "list":
			displayID, boardName, err := parseTicketRefArgs(cleaned[1:])
			if err != nil {
				return err
			}
			if displayID == "" {
				return fmt.Errorf("usage: kanbi notes list T-001 [--board NAME] [--json]")
			}
			ticket, err := cli.ResolveTicket(displayID, boardName)
			if err != nil {
				return err
			}
			notes, err := cli.store.ListNotes(ctx, ticket.ID)
			if err != nil {
				return err
			}
			if format.JSON {
				return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.notes", "ticket": cli.jsonTicket(ctx, ticket, false), "notes": jsonNotes(notes)})
			}
			for _, n := range notes {
				fmt.Printf("%d\t%s\n", n.ID, n.Body)
			}
			return nil
		case "add":
			opts, err := parseNoteAddArgs(cleaned[1:])
			if err != nil {
				return err
			}
			if opts.DisplayID == "" || strings.TrimSpace(opts.Body) == "" {
				return fmt.Errorf("usage: kanbi notes add T-001 --body BODY [--board NAME] [--json]")
			}
			ticket, err := cli.ResolveTicket(opts.DisplayID, opts.BoardName)
			if err != nil {
				return err
			}
			note, err := cli.store.AddNote(ctx, ticket.ID, opts.Body)
			if err != nil {
				return err
			}
			if format.JSON {
				return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.note", "ticket": cli.jsonTicket(ctx, ticket, false), "note": jsonNote(note)})
			}
			fmt.Printf("%d\t%s\n", note.ID, note.Body)
			return nil
		default:
			return fmt.Errorf("usage: kanbi notes [list|add] ...")
		}
	})
}

func runState(ctx context.Context, cfg config.Config, args []string) error {
	format, cleaned, err := parseFormat(args)
	if err != nil {
		return err
	}
	boardName, err := parseOptionalBoard(cleaned)
	if err != nil {
		return err
	}
	return withCLIContext(ctx, cfg, func(cli *cliContext) error {
		if !format.JSON {
			return runList(ctx, cfg, cleaned)
		}
		if boardName != "" {
			view, err := cli.ResolveBoardView(boardName)
			if err != nil {
				return err
			}
			return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.state", "boards": []any{cli.jsonBoardView(ctx, view)}})
		}
		boards, err := cli.store.ListBoards(ctx)
		if err != nil {
			return err
		}
		out := make([]any, 0, len(boards))
		for _, b := range boards {
			view, err := cli.store.BoardViewByID(ctx, b.ID)
			if err != nil {
				return err
			}
			out = append(out, cli.jsonBoardView(ctx, view))
		}
		return writeJSON(os.Stdout, map[string]any{"schema": "kanbi.v1.state", "boards": out})
	})
}

type outputFormat struct{ JSON bool }

type updateOptions struct {
	DisplayID string
	BoardName string
	Title     *string
	Body      *string
	Harness   *string
}

type noteAddOptions struct {
	DisplayID string
	BoardName string
	Body      string
}

func parseFormat(args []string) (outputFormat, []string, error) {
	format := outputFormat{}
	cleaned := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			format.JSON = true
		case "--format":
			i++
			if i >= len(args) {
				return format, nil, fmt.Errorf("--format requires a value")
			}
			switch args[i] {
			case "json":
				format.JSON = true
			case "text", "plain":
				format.JSON = false
			default:
				return format, nil, fmt.Errorf("unsupported format %q", args[i])
			}
		default:
			cleaned = append(cleaned, args[i])
		}
	}
	return format, cleaned, nil
}

func readValue(kind, value string) (string, error) {
	switch kind {
	case "literal":
		return value, nil
	case "file":
		data, err := os.ReadFile(value)
		return string(data), err
	case "stdin":
		data, err := io.ReadAll(os.Stdin)
		return string(data), err
	default:
		return "", fmt.Errorf("unknown value source %q", kind)
	}
}

func parseTicketRefArgs(args []string) (displayID, boardName string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--board":
			i++
			if i >= len(args) {
				return "", "", fmt.Errorf("--board requires a value")
			}
			boardName = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", fmt.Errorf("unknown flag %s", args[i])
			}
			if displayID != "" {
				return "", "", fmt.Errorf("accepts one display id")
			}
			displayID = args[i]
		}
	}
	return displayID, boardName, nil
}

func parseUpdateArgs(args []string) (updateOptions, error) {
	var opts updateOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--board":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--board requires a value")
			}
			opts.BoardName = args[i]
		case "--title":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--title requires a value")
			}
			opts.Title = &args[i]
		case "--body", "--body-file":
			flag := args[i]
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("%s requires a value", flag)
			}
			kind := "literal"
			if flag == "--body-file" {
				kind = "file"
			}
			body, err := readValue(kind, args[i])
			if err != nil {
				return opts, err
			}
			opts.Body = &body
		case "--body-stdin":
			body, err := readValue("stdin", "")
			if err != nil {
				return opts, err
			}
			opts.Body = &body
		case "--harness":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--harness requires a value")
			}
			opts.Harness = &args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("unknown update flag %s", args[i])
			}
			if opts.DisplayID != "" {
				return opts, fmt.Errorf("update accepts one display id")
			}
			opts.DisplayID = args[i]
		}
	}
	return opts, nil
}

func parseMoveArgs(args []string) (displayID, boardName, toColumn string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--board":
			i++
			if i >= len(args) {
				return "", "", "", fmt.Errorf("--board requires a value")
			}
			boardName = args[i]
		case "--to":
			i++
			if i >= len(args) {
				return "", "", "", fmt.Errorf("--to requires a value")
			}
			toColumn = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", "", fmt.Errorf("unknown move flag %s", args[i])
			}
			if displayID != "" {
				return "", "", "", fmt.Errorf("move accepts one display id")
			}
			displayID = args[i]
		}
	}
	return displayID, boardName, toColumn, nil
}

func parseNoteAddArgs(args []string) (noteAddOptions, error) {
	var opts noteAddOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--board":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--board requires a value")
			}
			opts.BoardName = args[i]
		case "--body", "--body-file":
			flag := args[i]
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("%s requires a value", flag)
			}
			kind := "literal"
			if flag == "--body-file" {
				kind = "file"
			}
			body, err := readValue(kind, args[i])
			if err != nil {
				return opts, err
			}
			opts.Body = body
		case "--body-stdin":
			body, err := readValue("stdin", "")
			if err != nil {
				return opts, err
			}
			opts.Body = body
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("unknown notes add flag %s", args[i])
			}
			if opts.DisplayID != "" {
				return opts, fmt.Errorf("notes add accepts one display id")
			}
			opts.DisplayID = args[i]
		}
	}
	return opts, nil
}

type cliContext struct {
	ctx     context.Context
	cfg     config.Config
	store   *storage.Store
	manager *tmux.Manager
}

func newCLIContext(ctx context.Context, cfg config.Config) (*cliContext, error) {
	store, err := openStore(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &cliContext{ctx: ctx, cfg: cfg, store: store, manager: tmux.NewManagerWithContext(ctx, cfg, store)}, nil
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
	if c.manager != nil {
		c.manager.Close()
	}
	return c.store.Close()
}

func (c *cliContext) Manager() *tmux.Manager {
	if c.manager == nil {
		c.manager = tmux.NewManagerWithContext(c.ctx, c.cfg, c.store)
	}
	return c.manager
}

func shouldAttachTmuxForBoard(cfg config.Config) bool {
	return strings.ToLower(strings.TrimSpace(cfg.Multiplexer.Default)) != "herdr"
}

func shouldLaunchHerdrBoard(cfg config.Config) bool {
	return strings.ToLower(strings.TrimSpace(cfg.Multiplexer.Default)) == "herdr" && os.Getenv("HERDR_ENV") != "1"
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

func (c *cliContext) jsonBoardView(ctx context.Context, view storage.BoardView) any {
	columns := make([]any, 0, len(view.Columns))
	for _, col := range view.Columns {
		tickets := make([]any, 0, len(col.Tickets))
		for _, ticket := range col.Tickets {
			tickets = append(tickets, c.jsonTicket(ctx, ticket, true))
		}
		columns = append(columns, map[string]any{"id": col.ID, "board_id": col.BoardID, "name": col.Name, "position": col.Position, "tickets": tickets})
	}
	return map[string]any{"board": jsonBoard(view.Board), "columns": columns}
}

func (c *cliContext) jsonTicket(ctx context.Context, t storage.Ticket, includeNotes bool) any {
	colName := c.columnName(ctx, t.BoardID, t.ColumnID)
	out := map[string]any{
		"id":          t.ID,
		"display_id":  t.DisplayID,
		"display_num": t.DisplayNum,
		"board": map[string]any{
			"id":      t.BoardID,
			"name":    t.BoardName,
			"workdir": t.BoardWorkdir,
		},
		"column": map[string]any{
			"id":   t.ColumnID,
			"name": colName,
		},
		"title":                 t.Title,
		"body":                  t.Body,
		"harness":               t.Harness,
		"runtime":               t.Runtime,
		"position":              t.Position,
		"archived_at":           nullTime(t.ArchivedAt),
		"external_id":           nullString(t.ExternalID),
		"external_url":          nullString(t.ExternalURL),
		"external_updated_at":   nullTime(t.ExternalUpdatedAt),
		"sync_version":          nullString(t.SyncVersion),
		"note_count":            t.NoteCount,
		"created_at":            t.CreatedAt,
		"updated_at":            t.UpdatedAt,
		"last_output_at":        nullTime(t.LastOutputAt),
		"last_state_change_at":  nullTime(t.LastStateChangeAt),
		"last_detected_state":   nullString(t.LastDetectedState),
		"last_attention_reason": nullString(t.LastAttentionReason),
		"last_detection_source": nullString(t.LastDetectionSource),
		"last_observed_excerpt": nullString(t.LastObservedExcerpt),
		"session":               nil,
	}
	if ses, ok, err := c.store.LatestSession(ctx, t.ID); err == nil && ok {
		out["session"] = jsonSession(ses)
	}
	if includeNotes {
		notes, err := c.store.ListNotes(ctx, t.ID)
		if err == nil {
			out["notes"] = jsonNotes(notes)
		}
	}
	return out
}

func (c *cliContext) columnName(ctx context.Context, boardID, columnID int64) string {
	view, err := c.store.BoardViewByID(ctx, boardID)
	if err != nil {
		return ""
	}
	for _, col := range view.Columns {
		if col.ID == columnID {
			return col.Name
		}
	}
	return ""
}

func jsonBoards(boards []storage.Board) []any {
	out := make([]any, 0, len(boards))
	for _, b := range boards {
		out = append(out, jsonBoard(b))
	}
	return out
}

func jsonBoard(b storage.Board) any {
	return map[string]any{
		"id":              b.ID,
		"name":            b.Name,
		"workdir":         b.Workdir,
		"ticket_backend":  b.TicketBackend,
		"backend_query":   b.BackendQuery,
		"backend_config":  b.BackendConfig,
		"last_sync_at":    nullTime(b.LastSyncAt),
		"last_sync_error": nullString(b.LastSyncError),
	}
}

func jsonSession(s storage.Session) any {
	return map[string]any{
		"id":                    s.ID,
		"ticket_id":             s.TicketID,
		"harness":               s.Harness,
		"harness_session_ref":   nullString(s.HarnessSessionRef),
		"harness_session_name":  nullString(s.HarnessSessionName),
		"multiplexer":           s.Multiplexer,
		"mux_namespace":         nullString(s.MuxNamespace),
		"mux_container_id":      nullString(s.MuxContainerID),
		"mux_container_name":    nullString(s.MuxContainerName),
		"mux_metadata":          nullString(s.MuxMetadata),
		"tmux_session_name":     s.TmuxSessionName,
		"tmux_window_id":        nullString(s.TmuxWindowID),
		"tmux_window_name":      s.TmuxWindowName,
		"status":                s.Status,
		"active":                s.IsActive,
		"started_at":            nullTime(s.StartedAt),
		"closed_at":             nullTime(s.ClosedAt),
		"last_seen_tmux_at":     nullTime(s.LastSeenTmuxAt),
		"last_output_at":        nullTime(s.LastOutputAt),
		"last_state_change_at":  nullTime(s.LastStateChangeAt),
		"last_detected_state":   nullString(s.LastDetectedState),
		"last_attention_reason": nullString(s.LastAttentionReason),
		"last_detection_source": nullString(s.LastDetectionSource),
		"last_observed_excerpt": nullString(s.LastObservedExcerpt),
	}
}

func jsonNotes(notes []storage.Note) []any {
	out := make([]any, 0, len(notes))
	for _, n := range notes {
		out = append(out, jsonNote(n))
	}
	return out
}

func jsonNote(n storage.Note) any {
	return map[string]any{
		"id":                  n.ID,
		"ticket_id":           n.TicketID,
		"external_id":         nullString(n.ExternalID),
		"external_updated_at": nullTime(n.ExternalUpdatedAt),
		"sync_version":        nullString(n.SyncVersion),
		"body":                n.Body,
		"created_at":          n.CreatedAt,
		"updated_at":          n.UpdatedAt,
	}
}

func nullString(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

func nullTime(v sql.NullTime) any {
	if !v.Valid {
		return nil
	}
	return v.Time
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
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
	return fmt.Errorf("unknown command %q\nusage: kanbi [doctor|boards|add|list|show|update|move|notes|state|sync|open|--board]", cmd)
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
		return storage.CreateBoardOptions{}, fmt.Errorf("usage: kanbi boards add \"Name\" [--cwd /path] [--backend local|github|atlassian] [--query QUERY] [--config JSON]")
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
		case "--body", "--body-file":
			flag := args[i]
			i++
			if i >= len(args) {
				return "", "", "", "", fmt.Errorf("%s requires a value", flag)
			}
			kind := "literal"
			if flag == "--body-file" {
				kind = "file"
			}
			body, err = readValue(kind, args[i])
			if err != nil {
				return "", "", "", "", err
			}
		case "--body-stdin":
			body, err = readValue("stdin", "")
			if err != nil {
				return "", "", "", "", err
			}
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
