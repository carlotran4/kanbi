package herdrcore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/multiplexer"
)

// Opt-in: owns a separate HC_SESSION; never inspects or stops the user's core.
func TestRealCore(t *testing.T) {
	if os.Getenv("KANBI_CORE_TESTS") != "1" {
		t.Skip("set KANBI_CORE_TESTS=1")
	}
	t.Setenv("HC_TERMINAL_ID", "")
	binary := os.Getenv("KANBI_CORE_BINARY")
	if binary == "" {
		binary = "hc"
	}
	p, err := exec.LookPath(binary)
	if err != nil {
		t.Fatal(err)
	}
	a := NewAdapter(Config{Binary: p, Session: fmt.Sprintf("kanbi-test-%d-%d", os.Getpid(), time.Now().UnixNano())})
	ctx := context.Background()
	t.Cleanup(func() {
		c := a.command(ctx, p, "server", "stop")
		if out, e := c.CombinedOutput(); e != nil {
			t.Errorf("stop owned core: %s %v", out, e)
		}
	})
	dir := t.TempDir()
	program := filepath.Join(dir, "agent.py")
	source := `import os,sys,tty,termios
old=termios.tcgetattr(0)
tty.setraw(0)
print('PROMPT_READY',flush=True)
print('\x1b]7501;state=blocked:app=kanbi-fixture:kind=permission\x1b\\',end='',flush=True)
while True:
 b=os.read(0,4096)
 if not b: break
 os.write(1,b)
`
	if err = os.WriteFile(program, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	ref, err := a.Launch(ctx, multiplexer.LaunchSpec{CWD: dir, Name: "Core verification", Command: []string{"python3", program}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(ctx, ref)
	deadline := time.Now().Add(10 * time.Second)
	for {
		d, e := a.Detect(ctx, ref)
		if e != nil {
			t.Fatal(e)
		}
		if d.State == "needs_permission" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native OSC status not detected", d)
		}
		time.Sleep(50 * time.Millisecond)
	}
	before, err := a.get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	text := "literal $(touch BAD) ; 'quotes'\nsecond line"
	if err = a.SendText(ctx, ref, text); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		out, e := a.Read(ctx, ref, multiplexer.ReadOptions{Lines: 20})
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(out, "literal $(touch BAD)") && strings.Contains(out, "second line") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("literal paste missing", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	after, err := a.get(ctx, ref)
	if err != nil || before.Cols != after.Cols || before.Rows != after.Rows || before.PID != after.PID {
		t.Fatal("input resized or replaced program", before, after, err)
	}
	var v any
	if err = a.RPC(ctx, "terminal.keep", map[string]any{"terminal": ref.ID, "keep": true}, &v); err != nil {
		t.Fatal(err)
	}
	if err = a.RPC(ctx, "terminal.keep", map[string]any{"terminal": ref.ID, "keep": false}, &v); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if ok, e := a.Validate(ctx, ref); e != nil || ok {
		t.Fatal(ok, e)
	}
	t.Log("independent launch, native permission, literal multiline paste, size/PID preservation, closure: PASS")
}
