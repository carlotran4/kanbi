package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/runtime"
	"github.com/carlotran4/kanbi/internal/storage"
)

func messageSessionID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("a positive Kanbi session ID is required")
	}
	return id, nil
}
func runMessages(ctx context.Context, cfg config.Config, args []string) error {
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	if len(args) == 1 && args[0] == "register-claude" {
		id, err := messageSessionID(os.Getenv("KANBI_SESSION_ID"))
		if err != nil {
			return err
		}
		return runtime.RegisterClaudeMessageEndpoint(ctx, store, id, os.Stdin)
	}
	manager := runtime.NewManagerWithContext(ctx, cfg, store)
	defer manager.Close()
	return executeMessages(ctx, store, os.Getenv("KANBI_SESSION_ID"), args, os.Stdout, func(ctx context.Context, recipient int64) error {
		notifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return manager.NotifySessionMessages(notifyCtx, recipient)
	})
}
func executeMessages(ctx context.Context, store *storage.Store, identity string, args []string, out io.Writer, notifiers ...func(context.Context, int64) error) error {
	if len(args) == 0 {
		return errors.New("usage: kanbi messages recipients|send|reply|inbox|ack|status (see --help)")
	}
	encode := func(v any) error { return json.NewEncoder(out).Encode(v) }
	if args[0] == "recipients" {
		if len(args) != 1 {
			return errors.New("usage: kanbi messages recipients")
		}
		recipients, err := store.MessageRecipients(ctx)
		if err != nil {
			return err
		}
		return encode(recipients)
	}
	sender, err := messageSessionID(identity)
	if err != nil {
		return errors.New("KANBI_SESSION_ID is required; run inside a newly launched Kanbi session")
	}
	session, found, err := store.SessionByID(ctx, sender)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("Kanbi sender session is missing")
	}
	switch args[0] {
	case "send", "reply":
		if len(args) < 2 {
			return errors.New("send needs a recipient ID; reply needs a message ID")
		}
		target, err := messageSessionID(args[1])
		if err != nil {
			return err
		}
		flags := flag.NewFlagSet("messages "+args[0], flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		key := flags.String("key", "", "stable retry key")
		body := flags.String("body", "", "message text")
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected message arguments")
		}
		recipient, replyTo := target, int64(0)
		if args[0] == "reply" {
			recipient = 0
			replyTo = target
		}
		message, err := store.SendSessionMessage(ctx, sender, recipient, *key, *body, replyTo)
		if err != nil {
			return err
		}
		if len(notifiers) > 0 {
			_ = notifiers[0](ctx, message.Recipient)
		}
		if updated, err := store.SessionMessage(ctx, message.ID); err == nil {
			message = updated
		}
		// This receipt describes persistence only. Never paste into the terminal or
		// wait for the recipient as part of sending.
		delivery := "unknown"
		endpoint, endpointErr := store.MessageRecipient(ctx, message.Recipient)
		if endpointErr == nil {
			delivery = endpoint.Delivery
		} else if errors.Is(endpointErr, sql.ErrNoRows) {
			delivery = "missing"
		}
		// Observation failures cannot turn an already-committed send into an
		// apparent failure; its durable receipt still lets the sender retry.
		return encode(struct {
			Message  storage.SessionMessage `json:"message"`
			Delivery string                 `json:"delivery"`
		}{message, delivery})
	case "claim", "defer-native", "retry":
		if len(args) != 2 {
			return errors.New("message ID required")
		}
		id, err := messageSessionID(args[1])
		if err != nil {
			return err
		}
		if args[0] == "claim" {
			claimed, err := runtime.ClaimNativeMessage(ctx, store, sender, id)
			if err != nil {
				return err
			}
			return encode(map[string]bool{"claimed": claimed})
		}
		if args[0] == "defer-native" {
			if session.Harness != "pi" {
				return errors.New("only Pi may defer a native claim")
			}
			if err := store.DeferNativeMessage(ctx, sender, id); err != nil {
				return err
			}
		} else {
			if err := store.RetryMessageNotification(ctx, sender, id); err != nil {
				return err
			}
			message, err := store.SessionMessage(ctx, id)
			if err != nil {
				return err
			}
			if len(notifiers) > 0 {
				_ = notifiers[0](ctx, message.Recipient)
			}
		}
		message, err := store.SessionMessage(ctx, id)
		if err != nil {
			return err
		}
		return encode(message)
	case "pending":
		if len(args) != 1 {
			return errors.New("usage: kanbi messages pending")
		}
		messages, err := store.SessionNotificationInbox(ctx, sender)
		if err != nil {
			return err
		}
		return encode(messages)
	case "inbox":
		all := len(args) == 2 && args[1] == "--all"
		if len(args) != 1 && !all {
			return errors.New("usage: kanbi messages inbox [--all]")
		}
		messages, err := store.SessionInbox(ctx, sender, all)
		if err != nil {
			return err
		}
		for i := range messages {
			if err := store.ReceiptMessageRead(ctx, sender, messages[i].ID); err != nil {
				return err
			}
			updated, err := store.SessionMessage(ctx, messages[i].ID)
			if err != nil {
				return err
			}
			messages[i] = updated
		}
		if len(notifiers) > 0 {
			_ = notifiers[0](ctx, sender)
		}
		return encode(messages)
	case "ack", "notified", "status":
		if len(args) != 2 {
			return fmt.Errorf("usage: kanbi messages %s MESSAGE_ID", args[0])
		}
		id, err := messageSessionID(args[1])
		if err != nil {
			return err
		}
		message, err := store.SessionMessage(ctx, id)
		if err != nil {
			return err
		}
		if args[0] == "status" {
			if message.Sender != sender && message.Recipient != sender {
				return errors.New("message does not belong to this session")
			}
			return encode(message)
		}
		if message.Recipient != sender {
			return errors.New("only the recipient may update delivery state")
		}
		if args[0] == "ack" {
			err = store.AcknowledgeSessionMessage(ctx, sender, id)
			if err == nil && len(notifiers) > 0 {
				_ = notifiers[0](ctx, sender)
			}
		} else {
			if !session.IsActive {
				return errors.New("closed sessions cannot accept notifications")
			}
			err = store.MarkMessageNotified(ctx, sender, id)
		}
		if err != nil {
			return err
		}
		message, err = store.SessionMessage(ctx, id)
		if err != nil {
			return err
		}
		return encode(message)
	default:
		return fmt.Errorf("unknown messages command %q", args[0])
	}
}
