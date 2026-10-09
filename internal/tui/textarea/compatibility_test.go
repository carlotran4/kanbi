package textarea

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/cursor"
	original "github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestWrapMatchesOriginalRules(t *testing.T) {
	rng := rand.New(rand.NewSource(358))
	alphabet := []rune(" abcdefXYZ0123456789!./*_~")
	for i := 0; i < 2000; i++ {
		n := rng.Intn(1200)
		runes := make([]rune, n)
		for j := range runes {
			runes[j] = alphabet[rng.Intn(len(alphabet))]
		}
		width := 1 + rng.Intn(120)
		if got, want := wrap(runes, width), wrapUnicode(runes, width); !reflect.DeepEqual(got, want) {
			t.Fatalf("case=%d width=%d text=%q\ngot=%q\nwant=%q", i, width, string(runes), got, want)
		}
	}
	for _, text := range []string{"界🙂 café e\u0301 👩‍💻", "tabs\tand\u00a0spaces", strings.Repeat("界", 400), "\n\x00"} {
		for _, w := range []int{1, 2, 3, 8, 80} {
			if !reflect.DeepEqual(wrap([]rune(text), w), wrapUnicode([]rune(text), w)) {
				t.Fatalf("unicode fallback changed: %q", text)
			}
		}
	}
}

func TestEditingAndVisibleFramesMatchUpstream(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	bodies := []string{"", "a", "exactly nineteen...", "\n\nblank\n\n", strings.Repeat("word **text** ", 180), strings.Repeat("Z", 1000), "界🙂 café e\u0301 👩‍💻\n" + strings.Repeat("Unicode 界 word ", 80)}
	keys := []tea.KeyMsg{
		{Type: tea.KeyCtrlHome}, {Type: tea.KeyCtrlEnd}, {Type: tea.KeyLeft}, {Type: tea.KeyRight}, {Type: tea.KeyUp}, {Type: tea.KeyDown},
		{Type: tea.KeyHome}, {Type: tea.KeyEnd}, {Type: tea.KeyBackspace}, {Type: tea.KeyDelete}, {Type: tea.KeyEnter},
		{Type: tea.KeyCtrlA}, {Type: tea.KeyCtrlE}, {Type: tea.KeyCtrlW}, {Type: tea.KeyCtrlK}, {Type: tea.KeyCtrlU},
		{Type: tea.KeyRunes, Runes: []rune("paste café 界\nnext e\u0301"), Paste: true},
		{Type: tea.KeyRunes, Runes: []rune("more ASCII words ")},
	}
	for _, numbered := range []bool{false, true} {
		for i, body := range bodies {
			t.Run(fmt.Sprintf("numbered=%t/body=%d", numbered, i), func(t *testing.T) {
				got, want := New(), original.New()
				got.ShowLineNumbers, want.ShowLineNumbers = numbered, numbered
				got.CharLimit, want.CharLimit = 0, 0
				got.SetValue(body)
				want.SetValue(body)
				got.Placeholder, want.Placeholder = "no description", "no description"
				got.SetWidth(23)
				want.SetWidth(23)
				got.SetHeight(4)
				want.SetHeight(4)
				got.Focus()
				want.Focus()
				got.Cursor.SetMode(cursor.CursorStatic)
				want.Cursor.SetMode(cursor.CursorStatic)
				check := func(step int) {
					t.Helper()
					if got.Value() != want.Value() || got.Line() != want.Line() || original.LineInfo(got.LineInfo()) != want.LineInfo() {
						t.Fatalf("state mismatch step=%d got=%q/%+v want=%q/%+v", step, got.Value(), got.LineInfo(), want.Value(), want.LineInfo())
					}
					if a, b := got.View(), want.View(); a != b {
						t.Fatalf("frame mismatch step=%d\ngot=%q\nwant=%q", step, a, b)
					}
				}
				check(-1)
				rng := rand.New(rand.NewSource(int64(358 + i)))
				for step := 0; step < 160; step++ {
					if step%19 == 0 {
						width := 8 + rng.Intn(65)
						height := 1 + rng.Intn(12)
						got.SetWidth(width)
						want.SetWidth(width)
						got.SetHeight(height)
						want.SetHeight(height)
					}
					key := keys[rng.Intn(len(keys))]
					got, _ = got.Update(key)
					want, _ = want.Update(key)
					check(step)
				}
				// Also retain upstream behavior for prompts whose content changes by row.
				got.SetPromptFunc(4, func(row int) string { return fmt.Sprintf("%03d ", row) })
				want.SetPromptFunc(4, func(row int) string { return fmt.Sprintf("%03d ", row) })
				check(160)
				got.Blur()
				want.Blur()
				check(161)
			})
		}
	}
}
