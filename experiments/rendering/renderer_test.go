package research

import (
	tea2 "charm.land/bubbletea/v2"
	"encoding/json"
	"fmt"
	tea1 "github.com/charmbracelet/bubbletea"
	"os"
	"sort"
	"sync"
	"testing"
	"time"
)

type toggle struct{}
type model1 struct {
	frames []string
	index  int
}

func (m model1) Init() tea1.Cmd { return nil }
func (m model1) Update(msg tea1.Msg) (tea1.Model, tea1.Cmd) {
	if _, ok := msg.(toggle); ok {
		m.index = 1 - m.index
	}
	return m, nil
}
func (m model1) View() string { return m.frames[m.index] }

type model2 struct {
	frames []string
	index  int
}

func (m model2) Init() tea2.Cmd { return nil }
func (m model2) Update(msg tea2.Msg) (tea2.Model, tea2.Cmd) {
	if _, ok := msg.(toggle); ok {
		m.index = 1 - m.index
	}
	return m, nil
}
func (m model2) View() tea2.View { return tea2.NewView(m.frames[m.index]) }

type sink struct {
	mu            sync.Mutex
	bytes, writes int
	chunks        []string
	notify        chan time.Time
}

func (s *sink) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.chunks = append(s.chunks, string(p))
	s.bytes += len(p)
	s.writes++
	s.mu.Unlock()
	select {
	case s.notify <- time.Now():
	default:
	}
	return len(p), nil
}
func TestRenderer(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	data, err := os.ReadFile("frames.json")
	if err != nil {
		t.Fatal(err)
	}
	var frames []string
	if err = json.Unmarshal(data, &frames); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || frames[0] == frames[1] {
		t.Fatal("need two distinct actual Kanbi frames")
	}
	for _, version := range []string{"v1", "v2"} {
		for _, fps := range []int{60, 120} {
			t.Run(fmt.Sprintf("%s/%dFPS", version, fps), func(t *testing.T) {
				s := &sink{notify: make(chan time.Time, 100)}
				var send func()
				var quit func()
				done := make(chan error, 1)
				if version == "v1" {
					p := tea1.NewProgram(model1{frames: frames}, tea1.WithInput(nil), tea1.WithOutput(s), tea1.WithFPS(fps), tea1.WithoutSignalHandler())
					send = func() { p.Send(toggle{}) }
					quit = p.Quit
					go func() { _, e := p.Run(); done <- e }()
					p.Send(tea1.WindowSizeMsg{Width: 160, Height: 40})
				} else {
					p := tea2.NewProgram(model2{frames: frames}, tea2.WithInput(nil), tea2.WithOutput(s), tea2.WithFPS(fps), tea2.WithWindowSize(160, 40), tea2.WithoutSignalHandler())
					send = func() { p.Send(toggle{}) }
					quit = p.Quit
					go func() { _, e := p.Run(); done <- e }()
					p.Send(tea2.WindowSizeMsg{Width: 160, Height: 40})
				}
				time.Sleep(100 * time.Millisecond)
				for len(s.notify) > 0 {
					<-s.notify
				}
				s.mu.Lock()
				s.bytes = 0
				s.writes = 0
				s.mu.Unlock()
				samples := make([]int64, 100)
				for i := range samples {
					time.Sleep(time.Duration(1+i%11) * time.Millisecond)
					at := time.Now()
					send()
					select {
					case written := <-s.notify:
						samples[i] = written.Sub(at).Nanoseconds()
					case <-time.After(time.Second):
						quit()
						t.Fatal("frame not written")
					}
				}
				s.mu.Lock()
				bytes, writes := s.bytes, s.writes
				s.mu.Unlock()
				time.Sleep(50 * time.Millisecond)
				s.mu.Lock()
				idleWrites := s.writes
				s.mu.Unlock()
				quit()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if idleWrites != writes {
					t.Fatalf("unchanged idle screen generated writes: before=%d after=%d last=%q", writes, idleWrites, s.chunks[len(s.chunks)-4:])
				}
				sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
				t.Logf("renderer=%s inline FPS=%d n=100 Send-to-write p50=%.3fms p95=%.3fms p99=%.3fms bytes/navigation=%.1f writes/navigation=%.2f", version, fps, float64(samples[49])/1e6, float64(samples[94])/1e6, float64(samples[98])/1e6, float64(bytes)/100, float64(writes)/100)
			})
		}
	}
}
