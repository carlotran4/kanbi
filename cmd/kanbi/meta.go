package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/carlotran4/kanbi/internal/buildinfo"
	"github.com/carlotran4/kanbi/internal/storage"
)

const rootHelp = `Kanbi supervises resumable coding-agent sessions on Kanban boards.

Usage:
  kanbi                         Open the interactive board picker
  kanbi COMMAND [ARGUMENTS]

Commands:
  boards     List, add, rename, or update boards
  add        Create a ticket
  list       List tickets
  show       Show one ticket
  update     Update ticket metadata
  move       Move a ticket to a workflow column
  notes      List, add, update, or delete ticket notes
  state      Show board/session state
  open       Open or resume a ticket agent session
  sync       Synchronize external ticket providers
  doctor     Check runtime prerequisites and configuration
  backup     Create a database-and-attachments backup
  restore    Restore a backup while Kanbi is stopped
  version    Show build and schema compatibility information
  help       Show this help or help for a command

Global options:
  -h, --help       Show help without creating configuration or data files
  -v, --version    Show version without creating configuration or data files

Examples:
  kanbi doctor
  kanbi boards add "My Project" --cwd "$PWD"
  kanbi add "Investigate failure" --body "Reproduce and fix"
  kanbi open T-001
  kanbi backup ~/kanbi-backup.kanbi

Run "kanbi help COMMAND" or "kanbi COMMAND --help" for command details.
Exit status is 0 for success/help and 1 for command, configuration, or runtime errors.
`

var commandHelp = map[string]string{
	"boards": `Usage: kanbi boards [--json]
       kanbi boards add NAME [--cwd PATH] [--backend local|github|atlassian] [--query QUERY] [--config JSON]
       kanbi boards rename OLD NEW
       kanbi boards set-cwd NAME PATH

List and manage boards. The TUI provides confirmed board deletion.`,
	"add": `Usage: kanbi add TITLE [--body TEXT | --body-file PATH] [--harness NAME] [--board NAME] [--json]

Create a ticket in the board's first column.`,
	"list": `Usage: kanbi list [--board NAME] [--json]

List tickets, including board context when listing multiple boards.`,
	"show": `Usage: kanbi show TICKET [--board NAME] [--json]

Show ticket metadata, notes, and session history. Use --board when an ID is ambiguous.`,
	"update": `Usage: kanbi update TICKET [--title TEXT] [--body TEXT | --body-file PATH] [--harness NAME] [--board NAME] [--json]

Update ticket metadata.`,
	"move": `Usage: kanbi move TICKET --to COLUMN [--board NAME] [--json]

Move a ticket within its owning board.`,
	"notes": `Usage: kanbi notes list TICKET [--board NAME] [--json]
       kanbi notes add TICKET --body TEXT [--board NAME] [--json]
       kanbi notes update NOTE_ID --body TEXT [--json]
       kanbi notes delete NOTE_ID [--json]

Manage durable ticket notes.`,
	"state": `Usage: kanbi state [--board NAME] [--json]

Show the current durable ticket/session projection.`,
	"open": `Usage: kanbi open TICKET [--board NAME] [--json]

Start, focus, resume, or repair a ticket session according to its durable lifecycle.`,
	"sync": `Usage: kanbi sync [--board NAME] [--json]

Synchronize GitHub or Jira ticket metadata for one or all boards.`,
	"doctor": `Usage: kanbi doctor [--json]

Check build/schema information, paths, configured multiplexer, tmux compatibility, terminal, and harness commands. Fatal results return an error.`,
	"backup": `Usage: kanbi backup PATH

Create a consistent, versioned archive containing SQLite state and attachments.`,
	"restore": `Usage: kanbi restore PATH [--force]

Restore a validated backup. Stop every Kanbi process first; --force replaces existing data.`,
	"version": `Usage: kanbi version [--json]
       kanbi --version

Show semantic version, commit, build date, Go version, platform, and supported database schema.`,
}

func handleMetadataCommand(args []string, stdout io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		command := ""
		if args[0] == "help" && len(args) > 1 {
			command = args[1]
		}
		return true, printHelp(stdout, command)
	}
	if args[0] == "-v" || args[0] == "--version" || args[0] == "version" {
		jsonOutput := false
		for _, arg := range args[1:] {
			if arg == "--json" || arg == "--format=json" {
				jsonOutput = true
			} else {
				return true, fmt.Errorf("usage: kanbi version [--json]")
			}
		}
		return true, printVersion(stdout, jsonOutput)
	}
	if len(args) > 1 && (args[1] == "-h" || args[1] == "--help") {
		return true, printHelp(stdout, args[0])
	}
	return false, nil
}

func printHelp(w io.Writer, command string) error {
	if command == "" {
		_, err := io.WriteString(w, rootHelp)
		return err
	}
	text, ok := commandHelp[strings.ToLower(command)]
	if !ok {
		return fmt.Errorf("unknown help topic %q", command)
	}
	_, err := fmt.Fprintf(w, "%s\n", text)
	return err
}

func printVersion(w io.Writer, jsonOutput bool) error {
	info := buildinfo.Current(storage.CurrentSchemaVersion())
	if jsonOutput {
		return json.NewEncoder(w).Encode(struct {
			Schema string `json:"schema"`
			buildinfo.Info
		}{Schema: "kanbi.v1.version", Info: info})
	}
	_, err := fmt.Fprintf(w, "kanbi %s\ncommit: %s\nbuilt: %s\ngo: %s\nplatform: %s/%s\ndatabase schema: %d\n", info.Version, info.Commit, info.BuildDate, info.GoVersion, info.OS, info.Arch, info.Schema)
	return err
}
