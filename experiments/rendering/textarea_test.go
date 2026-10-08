package research

import (
	next "charm.land/bubbles/v2/textarea"
	tea2 "charm.land/bubbletea/v2"
	"fmt"
	old "github.com/charmbracelet/bubbles/textarea"
	tea1 "github.com/charmbracelet/bubbletea"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func body(n int) string {
	return strings.Repeat("markdown **body** ", n/len("markdown **body** ")) + strings.Repeat("x", n%len("markdown **body** "))
}
func measure(t *testing.T, name string, step func(), check func() bool) {
	for range 3 {
		step()
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	samples := make([]int64, 100)
	for i := range samples {
		at := time.Now()
		step()
		samples[i] = time.Since(at).Nanoseconds()
	}
	runtime.ReadMemStats(&after)
	if !check() {
		t.Fatal("text value was truncated or lost")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	t.Logf("%s n=100 p50=%.3fms p95=%.3fms p99=%.3fms bytes/op=%d allocs/op=%d", name, float64(samples[49])/1e6, float64(samples[94])/1e6, float64(samples[98])/1e6, (after.TotalAlloc-before.TotalAlloc)/100, (after.Mallocs-before.Mallocs)/100)
}
func TestTextarea(t *testing.T) {
	for _, n := range []int{1024, 65536} {
		for _, multiline := range []bool{false, true} {
			text := body(n)
			if multiline {
				parts := make([]string, 32)
				for i := range parts {
					parts[i] = text[i*n/32 : (i+1)*n/32]
				}
				text = strings.Join(parts, "\n")
			}
			t.Run(fmt.Sprintf("bytes=%d/multiline=%t", n, multiline), func(t *testing.T) {
				a := old.New()
				a.CharLimit = 0
				a.ShowLineNumbers = false
				a.SetWidth(108)
				a.SetHeight(12)
				a.SetValue(text)
				a.Focus()
				a.CursorEnd()
				measure(t, "bubbles-v1 Update+View", func() { a, _ = a.Update(tea1.KeyMsg{Type: tea1.KeyRunes, Runes: []rune{'x'}}); _ = a.View() }, func() bool { return a.Value() == text+strings.Repeat("x", 103) })
				if n == 65536 {
					c := old.New()
					c.CharLimit = 0
					c.ShowLineNumbers = false
					c.SetWidth(108)
					c.SetHeight(12)
					c.SetValue(text)
					c.Focus()
					c.CursorEnd()
					measure(t, "bubbles-v1 Update only", func() { c, _ = c.Update(tea1.KeyMsg{Type: tea1.KeyRunes, Runes: []rune{'x'}}) }, func() bool { return c.Value() == text+strings.Repeat("x", 103) })
					measure(t, "bubbles-v1 warm View only", func() { _ = c.View() }, func() bool { return c.Value() == text+strings.Repeat("x", 103) })
				}
				b := next.New()
				b.CharLimit = 0
				b.ShowLineNumbers = false
				b.SetVirtualCursor(true)
				b.SetWidth(108)
				b.SetHeight(12)
				b.SetValue(text)
				b.Focus()
				b.CursorEnd()
				measure(t, "bubbles-v2 Update+View", func() { b, _ = b.Update(tea2.KeyPressMsg{Code: 'x', Text: "x"}); _ = b.View() }, func() bool { return b.Value() == text+strings.Repeat("x", 103) })
			})
		}
	}
}
