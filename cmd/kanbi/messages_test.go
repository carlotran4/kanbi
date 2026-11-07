package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/carlotran4/kanbi/internal/storage"
)

// A cross-harness mailbox prototype through the actual CLI command dispatcher,
// with no model, terminal injection, external credentials, or human relaying.
func TestMessagesCrossHarnessMailbox(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	if err = store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := store.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, h := range []string{"claude", "pi"} {
		ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, h, "", h)
		if err != nil {
			t.Fatal(err)
		}
		id, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: h, Status: "running", Multiplexer: "herdr"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	call := func(identity int64, args ...string) []byte {
		t.Helper()
		var out bytes.Buffer
		if err := executeMessages(ctx, store, fmt.Sprint(identity), args, &out); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	call(0, "recipients")
	sent := call(ids[0], "send", fmt.Sprint(ids[1]), "--key", "schema-ready", "--body", "Please update implementation")
	var receipt struct {
		Message storage.SessionMessage `json:"message"`
	}
	if err := json.Unmarshal(sent, &receipt); err != nil {
		t.Fatal(err)
	}
	// Reopen the database as a separate process would after Kanbi restarts.
	store.Close()
	store, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	var inbox []storage.SessionMessage
	if err = json.Unmarshal(call(ids[1], "inbox"), &inbox); err != nil || len(inbox) != 1 {
		t.Fatal(inbox, err)
	}
	call(ids[1], "notified", fmt.Sprint(receipt.Message.ID))
	call(ids[1], "ack", fmt.Sprint(receipt.Message.ID))
	call(ids[1], "reply", fmt.Sprint(receipt.Message.ID), "--key", "schema-done", "--body", "Implementation updated")
	if err = json.Unmarshal(call(ids[0], "inbox"), &inbox); err != nil || len(inbox) != 1 || inbox[0].Body != "Implementation updated" {
		t.Fatal(inbox, err)
	}
	if err = store.MarkSessionClosed(ctx, ids[1], "closed", "test", ""); err != nil {
		t.Fatal(err)
	}
	var offline struct {
		Delivery string                 `json:"delivery"`
		Message  storage.SessionMessage `json:"message"`
	}
	if err = json.Unmarshal(call(ids[0], "send", fmt.Sprint(ids[1]), "--key", "offline", "--body", "retained offline"), &offline); err != nil {
		t.Fatal(err)
	}
	if offline.Delivery != "closed_unresumable" || offline.Message.State != "queued" {
		t.Fatal(offline)
	}

	var out bytes.Buffer
	if err := executeMessages(ctx, store, "", []string{"inbox"}, &out); err == nil {
		t.Fatal("missing sender identity accepted")
	}
	if err := executeMessages(ctx, store, fmt.Sprint(ids[0]), []string{"ack", fmt.Sprint(receipt.Message.ID)}, &out); err == nil {
		t.Fatal("sender acknowledged recipient mail")
	}
}
