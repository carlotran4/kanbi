package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/carlotran4/kanbi/internal/config"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	herdrmux "github.com/carlotran4/kanbi/internal/multiplexer/herdr"
	"github.com/carlotran4/kanbi/internal/storage"
)

type fleetRunner struct {
	renamedTabs       []string
	renamedPanes      []string
	markers           map[int]string
	splits, failSplit int
	failSplitAfter    bool
	panes             map[string]herdrmux.FleetPane
	next, moves       int
	failMove          int
	failAfter         bool
	ratios            []string
}

func (r *fleetRunner) FleetRatio(_ context.Context, _, tab string, path []bool, ratio float64) error {
	r.ratios = append(r.ratios, fmt.Sprint(tab, path, ratio))
	return nil
}
func (r *fleetRunner) Run(_ context.Context, _ string, args ...string) (string, error) {
	result := map[string]any{}
	key := strings.Join(args[:min(2, len(args))], " ")
	switch key {
	case "status --json":
		return `{"server":{"running":true,"compatible":true,"socket":"/test/herdr.sock"}}`, nil
	case "workspace get":
		result["workspace"] = map[string]string{"label": "source workspace"}
	case "pane get":
		p, ok := r.panes[args[2]]
		if !ok {
			return `{"error":{"code":"pane_not_found"}}`, errors.New("absent")
		}
		result["pane"] = p
	case "pane list":
		panes := []herdrmux.FleetPane{}
		for _, p := range r.panes {
			panes = append(panes, p)
		}
		result["panes"] = panes
	case "pane layout":
		id := args[len(args)-1]
		p, ok := r.panes[id]
		if !ok {
			return "", errors.New("missing layout")
		}
		panes := []map[string]any{}
		for _, other := range r.panes {
			if other.Tab == p.Tab {
				panes = append(panes, map[string]any{"pane_id": other.ID, "rect": map[string]int{"width": 160, "height": 45}})
			}
		}
		result["layout"] = map[string]any{"area": map[string]int{"width": 161, "height": 45}, "panes": panes, "focused_pane_id": id}
	case "pane split":
		r.splits++
		if r.failSplit == r.splits && !r.failSplitAfter {
			return "", errors.New("injected split failure")
		}
		target := r.panes[args[2]]
		r.next++
		p := herdrmux.FleetPane{ID: fmt.Sprintf("%s:p%d", target.Workspace, r.next), Terminal: fmt.Sprintf("anchor-%d", r.next), Workspace: target.Workspace, Tab: target.Tab}
		r.panes[p.ID] = p
		if r.markers == nil {
			r.markers = map[int]string{}
		}
		for i, a := range args {
			if a == "--env" && i+1 < len(args) {
				r.markers[r.next] = strings.TrimPrefix(args[i+1], "KANBI_FLEET_ANCHOR=")
			}
		}
		if r.failSplit == r.splits && r.failSplitAfter {
			return "", errors.New("lost split reply")
		}
		result["pane"] = p
	case "pane process-info":
		p := r.panes[args[len(args)-1]]
		pid, _ := strconv.Atoi(strings.TrimPrefix(p.Terminal, "anchor-"))
		result["process_info"] = map[string]int{"shell_pid": pid}
	case "eww -p":
		return "sleep 2147483647 KANBI_FLEET_ANCHOR=" + r.markers[mustPID(args[2])], nil
	case "pane move":
		r.moves++
		if r.failMove == r.moves && !r.failAfter {
			return "", errors.New("injected move failure")
		}
		id := args[2]
		p, ok := r.panes[id]
		if !ok {
			return "", errors.New("missing source")
		}
		value := func(flag string) string {
			for i, a := range args {
				if a == flag && i+1 < len(args) {
					return args[i+1]
				}
			}
			return ""
		}
		target := r.panes[value("--target-pane")]
		if value("--tab") == "" {
			r.next++
			workspace := value("--workspace")
			if workspace == "" {
				workspace = fmt.Sprintf("recreated-w%d", r.next)
			}
			target = herdrmux.FleetPane{Workspace: workspace, Tab: fmt.Sprintf("%s:t%d", workspace, r.next)}
		}
		delete(r.panes, id)
		r.next++
		p.ID = fmt.Sprintf("%s:p%d", target.Workspace, r.next)
		p.Tab, p.Workspace = target.Tab, target.Workspace
		r.panes[p.ID] = p
		if r.failMove == r.moves && r.failAfter {
			return "", errors.New("injected ambiguous timeout after move")
		}
		result["move_result"] = map[string]any{"pane": p}
	case "pane close":
		delete(r.panes, args[2])
	case "agent get":
		return `{"result":{"agent":{"status":"working"}}}`, nil
	case "tab rename":
		r.renamedTabs = append(r.renamedTabs, args[2])
	case "pane rename":
		r.renamedPanes = append(r.renamedPanes, args[2])
	case "pane neighbor":
		result["neighbor"] = map[string]string{"neighbor_pane_id": "w1:p1"}
	case "tab focus", "pane report-metadata", "pane run", "pane focus", "pane zoom":
	default:
		return "", fmt.Errorf("unexpected Fleet command %q", args)
	}
	b, _ := json.Marshal(map[string]any{"result": result})
	return string(b), nil
}
func fleetTestManager(t *testing.T, n int) (*Manager, *fleetRunner, []int64) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kanbi.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := config.Defaults(config.Paths{})
	cfg.DBPath = path
	r := &fleetRunner{panes: map[string]herdrmux.FleetPane{}, next: 100}
	header := herdrmux.FleetPane{ID: "w1:p1", Terminal: "board-terminal", Workspace: "w1", Tab: "w1:t1"}
	r.panes[header.ID] = header
	view, err := store.BoardView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for i := 0; i < n; i++ {
		ticket, err := store.CreateTicket(ctx, view.Columns[0].ID, fmt.Sprintf("agent %d", i), "", "pi")
		if err != nil {
			t.Fatal(err)
		}
		p := herdrmux.FleetPane{ID: fmt.Sprintf("w%d:p1", i+2), Terminal: fmt.Sprintf("terminal-%d", i), Workspace: fmt.Sprintf("w%d", i+2), Tab: fmt.Sprintf("w%d:t1", i+2)}
		r.panes[p.ID] = p
		meta, _ := json.Marshal(map[string]string{"pane_id": p.ID, "tab_id": p.Tab, "terminal_id": p.Terminal})
		sid, err := store.UpsertActiveSession(ctx, ticket.ID, storage.Session{Harness: "pi", Multiplexer: "herdr", MuxNamespace: sql.NullString{String: p.Workspace, Valid: true}, MuxContainerID: sql.NullString{String: p.ID, Valid: true}, MuxMetadata: sql.NullString{String: string(meta), Valid: true}, HarnessSessionRef: sql.NullString{String: "verified-ref", Valid: true}, Status: "running"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sid)
	}
	m := NewManager(cfg, store)
	m.FleetAdapter = herdrmux.NewAdapter(herdrmux.Config{})
	m.FleetAdapter.Runner = r
	t.Setenv("HERDR_PANE_ID", header.ID)
	t.Setenv("HERDR_CONFIG_PATH", filepath.Join(t.TempDir(), "absent.toml"))
	t.Cleanup(m.Close)
	return m, r, ids
}
func TestFleetRoundTripPreservesAttemptsAndSourceTabs(t *testing.T) {
	for n := 3; n <= 6; n++ {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			m, r, ids := fleetTestManager(t, n)
			ctx := context.Background()
			original := map[string]herdrmux.FleetPane{}
			for k, v := range r.panes {
				original[k] = v
			}
			if err := m.EnterFleet(ctx, 0, 160, 45, `{"Selected":42}`); err != nil {
				t.Fatal(err)
			}
			j, err := m.loadFleet(ctx)
			if err != nil || j.Phase != "active" {
				t.Fatalf("journal %v %v", j, err)
			}
			for _, member := range j.Members {
				if member.Pane.Tab != j.Header.Tab {
					t.Fatal("member not gathered")
				}
				if _, ok := r.panes[member.Source.ID]; !ok {
					t.Fatal("source anchor disappeared")
				}
			}
			if len(r.ratios) == 0 {
				t.Fatal("ratios not applied")
			}
			if err := m.RestoreFleet(ctx); err != nil {
				t.Fatal(err)
			}
			if err := m.RestoreFleet(ctx); err != nil {
				t.Fatal("repeat return", err)
			}
			if len(r.panes) != len(original) {
				t.Fatalf("anchors leaked: %d panes", len(r.panes))
			}
			for _, member := range j.Members {
				p, ok, _ := fleetFind(mapPanes(r), member.Pane.Terminal)
				if !ok || p.Tab != member.OriginalTab() {
					t.Fatal("wrong destination")
				}
				ses, ok, err := m.Store.ActiveSession(ctx, member.Ticket)
				if err != nil || !ok || ses.ID != member.Session || ses.HarnessSessionRef.String != "verified-ref" {
					t.Fatalf("attempt/ref changed: %+v %v", ses, err)
				}
			}
			_ = ids
		})
	}
}
func mapPanes(r *fleetRunner) []herdrmux.FleetPane {
	p := []herdrmux.FleetPane{}
	for _, v := range r.panes {
		p = append(p, v)
	}
	return p
}
func TestFleetMoveFailureAndAmbiguousTimeoutRestoreTerminals(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			m, r, _ := fleetTestManager(t, 4)
			r.failMove = 2
			r.failAfter = after
			if err := m.EnterFleet(context.Background(), 0, 160, 45, ""); err == nil {
				t.Fatal("expected injected failure")
			}
			j, err := m.loadFleet(context.Background())
			if err != nil || j != nil {
				t.Fatalf("rollback journal %v %v", j, err)
			}
			if len(r.panes) != 5 {
				t.Fatalf("leaked panes: %d", len(r.panes))
			}
			for _, p := range r.panes {
				if p.Terminal != "board-terminal" && p.Tab == "w1:t1" {
					t.Fatal("agent left gathered")
				}
			}
		})
	}
}
func TestFleetRecoveryAfterReferencePersistenceGap(t *testing.T) {
	m, r, _ := fleetTestManager(t, 3)
	ctx := context.Background()
	if err := m.EnterFleet(ctx, 0, 160, 45, "saved-view"); err != nil {
		t.Fatal(err)
	}
	j, _ := m.loadFleet(ctx)
	member := &j.Members[0]
	member.Pane.ID = "stale-public-id"
	j.Phase = "entering"
	if err := m.saveFleet(ctx, j, false); err != nil {
		t.Fatal(err)
	}
	m.releaseFleetOwner()
	if err := m.Reconcile(ctx); err == nil {
		t.Fatal("pending journal must block normal reconciliation")
	}
	if err := m.RestoreFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.panes) != 4 {
		t.Fatal("recovery leaked anchors")
	}
}
func TestFleetDoesNotMoveNewAttemptOrReplacementPane(t *testing.T) {
	m, r, _ := fleetTestManager(t, 3)
	ctx := context.Background()
	if err := m.EnterFleet(ctx, 0, 160, 45, ""); err != nil {
		t.Fatal(err)
	}
	j, _ := m.loadFleet(ctx)
	member := j.Members[0]
	p := r.panes[member.Pane.ID]
	delete(r.panes, p.ID)
	replacement := p
	replacement.Terminal = "unrelated-replacement"
	replacement.Tab = "other-tab"
	r.panes[p.ID] = replacement
	sid, err := m.Store.UpsertActiveSession(ctx, member.Ticket, storage.Session{Harness: "pi", Multiplexer: "herdr", MuxContainerID: sql.NullString{String: "new-agent", Valid: true}, Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.RestoreFleet(ctx); err != nil {
		t.Fatal(err)
	}
	ses, _, _ := m.Store.ActiveSession(ctx, member.Ticket)
	if ses.ID != sid || r.panes[p.ID].Terminal != "unrelated-replacement" {
		t.Fatal("new attempt or replacement changed")
	}
}
func TestFleetOwnerAndRuntimeLocksAcrossManagers(t *testing.T) {
	m, _, _ := fleetTestManager(t, 3)
	other := NewManager(m.Config, m.Store)
	t.Cleanup(other.Close)
	if err := m.acquireFleetOwner(); err != nil {
		t.Fatal(err)
	}
	if err := other.acquireFleetOwner(); err == nil {
		t.Fatal("second owner accepted")
	}
	_, unlock, err := m.lockRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, _, err = other.lockRuntime(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent runtime lock %v", err)
	}
}
func TestFleetRejectsSmallAndSharedTabsWithoutMoves(t *testing.T) {
	m, r, _ := fleetTestManager(t, 6)
	if _, err := m.PreviewFleet(context.Background(), 0, 80, 24); err == nil {
		t.Fatal("small pane layout admitted")
	}
	if r.moves != 0 || len(r.panes) != 7 {
		t.Fatal("preflight mutated terminals")
	}
	header := r.panes["w1:p1"]
	header.ID = "w1:p2"
	header.Terminal = "unrelated"
	r.panes[header.ID] = header
	if _, err := m.PreviewFleet(context.Background(), 0, 160, 45); err == nil {
		t.Fatal("shared board tab admitted")
	}
}

func TestFleetRestoresToRecreatedSourcesAndRetriesAmbiguousReturn(t *testing.T) {
	m, r, _ := fleetTestManager(t, 3)
	ctx := context.Background()
	if err := m.EnterFleet(ctx, 0, 160, 45, ""); err != nil {
		t.Fatal(err)
	}
	j, _ := m.loadFleet(ctx)
	delete(r.panes, j.Members[0].Source.ID)
	r.failMove = r.moves + 1
	r.failAfter = true
	if err := m.RestoreFleet(ctx); err == nil {
		t.Fatal("expected ambiguous recreated return")
	}
	if err := m.RestoreFleet(ctx); err != nil {
		t.Fatal(err)
	}
	member := j.Members[0]
	p, ok, _ := fleetFind(mapPanes(r), member.Pane.Terminal)
	if !ok || p.Workspace == "w1" || !strings.HasPrefix(p.Workspace, "recreated-") {
		t.Fatalf("source not recreated: %+v", p)
	}
	if len(r.panes) != 4 {
		t.Fatal("anchor leak")
	}
}
func TestFleetNativeAgentTargetSurvivesPaneIDChanges(t *testing.T) {
	m, _, _ := fleetTestManager(t, 3)
	ctx := context.Background()
	tickets, err := m.Store.ListTickets(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	ses, _, _ := m.Store.ActiveSession(ctx, tickets[0].ID)
	ses.MuxContainerID = sql.NullString{String: "native-agent", Valid: true}
	sid, err := m.Store.UpsertActiveSession(ctx, tickets[0].ID, ses)
	if err != nil {
		t.Fatal(err)
	}
	for _, enter := range []bool{true, false} {
		if enter {
			err = m.EnterFleet(ctx, 0, 160, 45, "")
		} else {
			err = m.RestoreFleet(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		fresh, _, err := m.Store.ActiveSession(ctx, tickets[0].ID)
		if err != nil || fresh.ID != sid || fresh.MuxContainerID.String != "native-agent" || fresh.HarnessSessionRef.String != "verified-ref" {
			t.Fatalf("native target/ref changed: %+v %v", fresh, err)
		}
	}
}

func TestFleetNativeBindingsRespectLocalConfig(t *testing.T) {
	for _, tc := range []struct {
		name, config, focus, zoom string
		invalid                   bool
	}{
		{"defaults", "", "ctrl+b then tab", "ctrl+b then z", false},
		{"custom literal strings", "[keys] # configured locally\nprefix = 'f12'\ncycle_pane_next = 'prefix+n'\nzoom = 'prefix+z'\n", "f12 then n", "f12 then z", false},
		{"direct with disabled prefix", "[keys]\nprefix = \"\"\ncycle_pane_next = \"ctrl+alt+n\"\nzoom = \"ctrl+alt+z\"\n", "ctrl+alt+n", "ctrl+alt+z", false},
		{"legacy fullscreen", "[keys]\nfullscreen = 'prefix+x'\n", "ctrl+b then tab", "ctrl+b then x", false},
		{"disabled cycle", "[keys]\ncycle_pane_next = ''\n", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_CONFIG_PATH", path)
			focus, zoom, err := fleetBindings()
			if (err != nil) != tc.invalid || !tc.invalid && (focus != tc.focus || zoom != tc.zoom) {
				t.Fatalf("bindings %q %q %v", focus, zoom, err)
			}
		})
	}
}

func mustPID(s string) int { p, _ := strconv.Atoi(s); return p }
func TestFleetLostSplitReplyUsesCreationNonceAndLeavesNoOrphans(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			m, r, _ := fleetTestManager(t, 3)
			r.failSplit = 2
			r.failSplitAfter = after
			if err := m.EnterFleet(context.Background(), 0, 160, 45, ""); err == nil {
				t.Fatal("expected split failure")
			}
			j, err := m.loadFleet(context.Background())
			if err != nil || j != nil || len(r.panes) != 4 {
				t.Fatalf("orphaned split: %v %v %d", j, err, len(r.panes))
			}
		})
	}
}
func TestFleetKilledDuringSplitPersistenceGapRecoversOwnedAnchor(t *testing.T) {
	m, r, _ := fleetTestManager(t, 3)
	ctx := context.Background()
	if err := m.EnterFleet(ctx, 0, 160, 45, ""); err != nil {
		t.Fatal(err)
	}
	j, _ := m.loadFleet(ctx)
	intent := &fleetAnchorIntent{Marker: j.Token + "-pending"}
	for _, p := range r.panes {
		intent.Before = append(intent.Before, p.Terminal)
	}
	j.PendingAnchor = intent
	j.Phase = "entering"
	if err := m.saveFleet(ctx, j, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.herdrAdapter().FleetSplit(ctx, j.Header.ID, "down", .5, intent.Marker); err != nil {
		t.Fatal(err)
	}
	m.releaseFleetOwner()
	if err := m.RestoreFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.panes) != 4 {
		t.Fatal("unrecorded anchor leaked")
	}
}
func TestRuntimeLockSurvivesParentDescriptorCloseWhileCLIIsRunning(t *testing.T) {
	m, _, _ := fleetTestManager(t, 1)
	ctx, unlock, err := m.lockRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if multiplexer.OperationLock(ctx) == nil {
		t.Fatal("no inheritable lock")
	}
	child := exec.Command("sh", "-c", "sleep 0.25")
	child.ExtraFiles = []*os.File{multiplexer.OperationLock(ctx)}
	if err := child.Start(); err != nil {
		unlock()
		t.Fatal(err)
	}

	unlock() // models parent termination: its descriptor is gone, child's remains.
	other := NewManager(m.Config, m.Store)
	t.Cleanup(other.Close)
	limited, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, release, err := other.lockRuntime(limited); err == nil {
		release()
		t.Fatal("in-flight CLI lost runtime ownership")
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	_, release, err := other.lockRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestFleetUnverifiedPendingAnchorRetainsJournalAndNeverClosesCandidate(t *testing.T) {
	m, r, _ := fleetTestManager(t, 3)
	ctx := context.Background()
	if err := m.EnterFleet(ctx, 0, 160, 45, ""); err != nil {
		t.Fatal(err)
	}
	j, _ := m.loadFleet(ctx)
	intent := &fleetAnchorIntent{Marker: j.Token + "-expected", TargetTab: j.Tab}
	for _, p := range r.panes {
		intent.Before = append(intent.Before, p.Terminal)
	}
	j.PendingAnchor = intent
	j.Phase = "entering"
	if err := m.saveFleet(ctx, j, false); err != nil {
		t.Fatal(err)
	}
	p, err := m.herdrAdapter().FleetSplit(ctx, j.Header.ID, "down", .5, "other-origin")
	if err != nil {
		t.Fatal(err)
	}
	m.releaseFleetOwner()
	if err = m.RestoreFleet(ctx); err == nil {
		t.Fatal("unverified creation accepted")
	}
	if _, ok := r.panes[p.ID]; !ok {
		t.Fatal("unverified terminal closed")
	}
	if journal, _ := m.loadFleet(ctx); journal == nil {
		t.Fatal("recovery discarded")
	}
	r.markers[mustPID(strings.TrimPrefix(p.Terminal, "anchor-"))] = intent.Marker
	if err = m.RestoreFleet(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestFleetRenamePreservesBoardTabAndRestoresUpdatedAgentLabel(t *testing.T) {
	m, r, _ := fleetTestManager(t, 3)
	ctx := context.Background()
	if err := m.EnterFleet(ctx, 0, 160, 45, ""); err != nil {
		t.Fatal(err)
	}
	j, _ := m.loadFleet(ctx)
	member := j.Members[0]
	ticket, err := m.Store.TicketByID(ctx, member.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.RenameTicketWindow(ctx, ticket, "new title"); err != nil {
		t.Fatal(err)
	}
	if len(r.renamedTabs) != 0 || len(r.renamedPanes) != 1 {
		t.Fatal("Fleet rename changed board tab")
	}
	if err = m.RestoreFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.renamedTabs) != 1 || r.renamedTabs[0] != member.OriginalTab() {
		t.Fatalf("source tab label not restored %v", r.renamedTabs)
	}
}

func TestFleetLiveOwnerDoesNotOfferRecoveryInAnotherKanbi(t *testing.T) {
	m, _, _ := fleetTestManager(t, 3)
	ctx := context.Background()
	if err := m.EnterFleet(ctx, 0, 160, 45, ""); err != nil {
		t.Fatal(err)
	}
	other := NewManager(m.Config, m.Store)
	other.FleetAdapter = m.FleetAdapter
	t.Cleanup(other.Close)
	status, err := other.FleetStatus(ctx)
	if err != nil || !status.Present || status.Recovery {
		t.Fatalf("live owner recovery offered: %+v %v", status, err)
	}
	if _, err = other.PreviewFleet(ctx, 0, 160, 45); err == nil {
		t.Fatal("another owner preview accepted")
	}
	m.releaseFleetOwner()
	status, err = other.FleetStatus(ctx)
	if err != nil || !status.Recovery {
		t.Fatalf("dead owner recovery missing: %+v %v", status, err)
	}
	if err = other.RestoreFleet(ctx); err != nil {
		t.Fatal(err)
	}
}
