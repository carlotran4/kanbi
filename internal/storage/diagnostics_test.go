package storage

import (
	"context"
	"strings"
	"testing"
)

func TestRedactSecretTextStripsTokens(t *testing.T) {
	in := "Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz012345 and token=sekrit api_token=abc123"
	out := RedactSecretText(in)
	if strings.Contains(out, "ghp_") || strings.Contains(out, "sekrit") || strings.Contains(out, "abc123") {
		t.Fatalf("not redacted: %q", out)
	}
}

func TestRuntimeDiagnosticsRoundTripAndCap(t *testing.T) {
	ctx := context.Background()
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertRuntimeDiagnostic(ctx, RuntimeDiagnosticInput{
		Kind:      DiagnosticKindReconcile,
		Operation: "startup_reconcile",
		Message:   "runtime reconciliation degraded (local data available)",
		Cause:     "Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz012345",
		Attempt:   1,
	}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListRuntimeDiagnostics(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d", len(list))
	}
	if strings.Contains(list[0].Cause, "ghp_") {
		t.Fatalf("cause not redacted: %q", list[0].Cause)
	}
	board, err := s.CreateBoard(ctx, "Diag")
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.BoardViewByID(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := s.CreateTicket(ctx, view.Columns[0].ID, "push", "body", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkTicketRemotePushPending(ctx, ticket.ID, "abcdefabcdefabcdefabcdefabcdefab"); err != nil {
		t.Fatal(err)
	}
	got, err := s.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.RemotePushState.Valid || got.RemotePushState.String != RemotePushStatePending {
		t.Fatalf("push state=%+v", got.RemotePushState)
	}
	if err := s.ClearTicketRemotePush(ctx, ticket.ID); err != nil {
		t.Fatal(err)
	}
}
