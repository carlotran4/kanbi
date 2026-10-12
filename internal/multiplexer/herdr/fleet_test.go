package herdr

import (
	"context"
	"errors"
	"testing"

	"github.com/carlotran4/kanbi/internal/multiplexer"
)

func TestFleetTerminalIdentityRejectsRecycledPublicIDsBeforeInputOrClose(t *testing.T) {
	for _, operation := range []string{"read", "focus", "text", "keys", "close", "detect", "validate"} {
		t.Run(operation, func(t *testing.T) {
			r := &fakeRunner{out: map[string]string{"pane get w1:p1": `{"result":{"pane":{"pane_id":"w1:p1","terminal_id":"replacement-terminal"}}}`}, err: map[string]error{}}
			a := NewAdapter(Config{})
			a.Runner = r
			ref := multiplexer.ContainerRef{ID: "native-agent", Metadata: `{"pane_id":"w1:p1","terminal_id":"original-terminal"}`}
			ctx := context.Background()
			var err error
			switch operation {
			case "read":
				_, err = a.Read(ctx, ref, multiplexer.ReadOptions{})
			case "focus":
				err = a.Focus(ctx, ref)
			case "text":
				err = a.SendText(ctx, ref, "dangerous input")
			case "keys":
				err = a.SendKeys(ctx, ref, "ctrl+c")
			case "close":
				err = a.Close(ctx, ref)
			case "detect":
				_, err = a.Detect(ctx, ref)
			case "validate":
				var valid bool
				valid, err = a.Validate(ctx, ref)
				if valid || err != nil {
					t.Fatalf("invalid terminal validated: %v %v", valid, err)
				}
				return
			}
			if !errors.Is(err, multiplexer.ErrContainerNotFound) {
				t.Fatalf("identity error %v", err)
			}
			if len(r.calls) != 1 {
				t.Fatalf("operation reached replacement: %v", r.calls)
			}
		})
	}
}
func TestFleetPaneExplicitAbsenceDiffersFromObservationFailure(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "absent", false: "timeout"}[missing], func(t *testing.T) {
			out := `{"error":{"code":"pane_not_found"}}`
			if !missing {
				out = "temporary socket timeout"
			}
			r := &fakeRunner{out: map[string]string{"pane get w1:p1": out}, err: map[string]error{"pane get w1:p1": errors.New("exit status 1")}}
			a := NewAdapter(Config{})
			a.Runner = r
			_, err := a.FleetPane(context.Background(), "w1:p1")
			if errors.Is(err, multiplexer.ErrContainerNotFound) != missing {
				t.Fatal(err)
			}
		})
	}
}
