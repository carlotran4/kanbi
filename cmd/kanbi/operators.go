package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/storage"
)

func runOperators(ctx context.Context, cfg config.Config, args []string) error {
	return withCLIContext(ctx, cfg, func(c *cliContext) error {
		if len(args) == 0 || args[0] == "list" {
			ops, e := c.Service().ListOperators(ctx)
			if e != nil {
				return e
			}
			return writeJSON(os.Stdout, ops)
		}
		if args[0] == "create" || args[0] == "edit" {
			edit := args[0] == "edit"
			o := storage.Operator{Harness: cfg.DefaultHarness, Timezone: "UTC"}
			rest := args[1:]
			if edit {
				if len(rest) == 0 {
					return errors.New("operator ID required")
				}
				id, e := strconv.ParseInt(rest[0], 10, 64)
				if e != nil {
					return e
				}
				o, e = c.store.Operator(ctx, id)
				if e != nil {
					return e
				}
				rest = rest[1:]
			}
			f := flag.NewFlagSet("operators", flag.ContinueOnError)
			f.SetOutput(io.Discard)
			name := f.String("name", o.Name, "name")
			h := f.String("harness", o.Harness, "immutable harness")
			cwd := f.String("cwd", o.CWD, "working directory")
			cron := f.String("schedule", o.Cron, "five-field cron or 15m/hourly/daily")
			zone := f.String("timezone", o.Timezone, "IANA timezone")
			prompt := f.String("prompt", o.ScheduledPrompt, "scheduled-turn prompt")
			instructions := f.String("instructions", "", "standing instructions")
			file := f.String("instructions-file", "", "read standing instructions from file")
			if e := f.Parse(rest); e != nil {
				return e
			}
			if f.NArg() != 0 {
				return errors.New("unexpected Operator arguments")
			}
			text := *instructions
			provided := false
			f.Visit(func(v *flag.Flag) {
				if v.Name == "instructions" || v.Name == "instructions-file" {
					provided = true
				}
			})
			if *file != "" {
				b, e := os.ReadFile(*file)
				if e != nil {
					return e
				}
				text = string(b)
			} else if edit && !provided {
				var e error
				text, e = c.Manager().ReadOperatorInstructions(o.ID)
				if e != nil {
					return e
				}
			}
			o.Name, o.Harness, o.CWD, o.Cron, o.Timezone, o.ScheduledPrompt = *name, *h, *cwd, *cron, *zone, *prompt
			saved, e := c.Manager().SaveOperator(ctx, o, text)
			if e != nil {
				return e
			}
			return writeJSON(os.Stdout, saved)
		}
		if args[0] == "wakeups" {
			if len(args) < 3 {
				return errors.New("usage: operators wakeups list OPERATOR_ID | create OPERATOR_ID --at RFC3339 --prompt TEXT | cancel WAKEUP_ID")
			}
			id, e := strconv.ParseInt(args[2], 10, 64)
			if e != nil || id <= 0 {
				return errors.New("positive ID required")
			}
			switch args[1] {
			case "list":
				v, e := c.store.OperatorWakeups(ctx, id)
				if e != nil {
					return e
				}
				return writeJSON(os.Stdout, v)
			case "cancel":
				return c.store.CancelOperatorWake(ctx, id)
			case "create":
				f := flag.NewFlagSet("wakeups create", flag.ContinueOnError)
				f.SetOutput(io.Discard)
				at := f.String("at", "", "RFC3339 future time")
				prompt := f.String("prompt", "", "follow-up prompt")
				if e = f.Parse(args[3:]); e != nil {
					return e
				}
				due, e := time.Parse(time.RFC3339, *at)
				if e != nil {
					return e
				}
				if !due.After(time.Now()) {
					return errors.New("wakeup must be in the future")
				}
				wid, e := c.store.QueueOperatorWake(ctx, id, "one-time follow-up", *prompt, due)
				if e != nil {
					return e
				}
				return writeJSON(os.Stdout, map[string]int64{"id": wid})
			default:
				return errors.New("unknown wakeups command")
			}
		}
		if len(args) != 2 {
			return errors.New("usage: operators open|run|pause|resume|stop|close|fresh|retry|delete ID")
		}
		id, e := strconv.ParseInt(args[1], 10, 64)
		if e != nil || id <= 0 {
			return errors.New("positive Operator ID required")
		}
		m := c.Manager()
		switch args[0] {
		case "open":
			return m.OpenOperator(ctx, id, false)
		case "run":
			if e = c.store.SetOperatorError(ctx, id, ""); e != nil {
				return e
			}
			return m.RunOperator(ctx, id, "")
		case "pause":
			return m.PauseOperator(ctx, id, true)
		case "resume":
			return m.PauseOperator(ctx, id, false)
		case "stop":
			return m.StopOperator(ctx, id)
		case "close":
			t, e := c.store.OperatorTicket(ctx, id)
			if e != nil {
				return e
			}
			return m.CloseSession(ctx, t)
		case "fresh":
			if e = m.OpenOperator(ctx, id, true); e != nil {
				return e
			}
			return c.store.SetOperatorError(ctx, id, "")
		case "retry":
			return c.store.SetOperatorError(ctx, id, "")
		case "delete":
			return c.store.DeleteOperator(ctx, id)
		default:
			return fmt.Errorf("unknown operators command %q", args[0])
		}
	})
}
