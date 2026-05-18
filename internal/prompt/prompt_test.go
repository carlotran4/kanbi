package prompt

import "testing"

func TestRenderPrompt(t *testing.T) {
	got := Render("T-001", "Title", "Body")
	want := "# T-001: Title\n\nBody"
	if got != want {
		t.Fatalf("prompt = %q", got)
	}
	got = Render("T-001", "Title", "")
	want = "# T-001: Title\n\nTitle"
	if got != want {
		t.Fatalf("empty body prompt = %q", got)
	}
	got = Render("T-001", "Title", "one\n\ntwo")
	want = "# T-001: Title\n\none\n\ntwo"
	if got != want {
		t.Fatalf("multiline prompt = %q", got)
	}
}
