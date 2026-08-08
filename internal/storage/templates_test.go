package storage

import "testing"

func TestTicketTemplateCRUDAndSnapshotApplication(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
	tmpl, err := s.CreateTicketTemplate(ctx, view.Board.ID, "Bug report", "Bug:", "## Reproduction\n", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTicketTemplate(ctx, view.Board.ID, "bug REPORT", "", "", "pi"); err == nil {
		t.Fatal("expected case-insensitive duplicate name rejection")
	} else if err.Error() != "template name already exists on this board" {
		t.Fatalf("duplicate error=%q", err)
	}
	body := "override body"
	ticket, err := s.CreateTicketFromTemplate(ctx, view.Columns[0].ID, tmpl.ID, TemplateTicketOverrides{Title: "Bug: resize", Body: &body, Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Title != "Bug: resize" || ticket.Body != body || ticket.Harness != "codex" {
		t.Fatalf("unexpected ticket snapshot: %+v", ticket)
	}
	if err := s.UpdateTicketTemplate(ctx, tmpl.ID, "Defect", "Changed", "changed", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTicketTemplate(ctx, tmpl.ID); err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.TicketByID(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Title != ticket.Title || unchanged.Body != ticket.Body || unchanged.Harness != ticket.Harness {
		t.Fatalf("template mutation changed existing ticket: %+v", unchanged)
	}
}

func TestTicketTemplatesAreBoardScopedAndCascadeWithBoard(t *testing.T) {
	s, ctx := newTestStore(t)
	first := defaultBoardView(t, ctx, s)
	secondBoard, err := s.CreateBoardWithWorkdir(ctx, "Other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.BoardViewByID(ctx, secondBoard.ID)
	if err != nil {
		t.Fatal(err)
	}
	one, err := s.CreateTicketTemplate(ctx, first.Board.ID, "Bug", "", "first", "pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTicketTemplate(ctx, second.Board.ID, "Bug", "", "second", "pi"); err != nil {
		t.Fatal("same template name should be allowed on another board:", err)
	}
	if _, err := s.CreateTicketFromTemplate(ctx, second.Columns[0].ID, one.ID, TemplateTicketOverrides{}); err == nil {
		t.Fatal("expected cross-board template application rejection")
	}
	if err := s.DeleteBoard(ctx, first.Board.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TicketTemplateByID(ctx, one.ID); err == nil {
		t.Fatal("expected board deletion to cascade template")
	}
}

func TestTicketTemplateFallbackTitleAndValidation(t *testing.T) {
	s, ctx := newTestStore(t)
	view := defaultBoardView(t, ctx, s)
	if _, err := s.CreateTicketTemplate(ctx, view.Board.ID, " ", "", "", "pi"); err == nil {
		t.Fatal("expected blank name rejection")
	}
	if _, err := s.CreateTicketTemplate(ctx, view.Board.ID, "Bad", "", "", "unknown"); err == nil {
		t.Fatal("expected harness rejection")
	}
	tmpl, err := s.CreateTicketTemplate(ctx, view.Board.ID, "Investigation", "", "details", "pi")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := s.CreateTicketFromTemplate(ctx, view.Columns[0].ID, tmpl.ID, TemplateTicketOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Title != "Investigation" {
		t.Fatalf("fallback title = %q", ticket.Title)
	}
}
