package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/kanban"
	"github.com/carlotran4/kanbi/internal/multiplexer"
	herdrmux "github.com/carlotran4/kanbi/internal/multiplexer/herdr"
)

type fleetMember struct {
	Ticket, Session int64
	Board           int64
	Label           string
	WorkspaceLabel  string
	OriginalTabID   string
	Original        multiplexer.ContainerRef
	Pane            herdrmux.FleetPane
	Source, Slot    herdrmux.FleetPane
	Restored        bool
	RestoreNew      bool
}
type fleetRatio struct {
	Path  []bool
	Ratio float64
	Thin  bool
}
type fleetAnchorIntent struct {
	TargetTab string
	Marker    string
	Before    []string
}
type fleetJournal struct {
	PendingAnchor                     *fleetAnchorIntent
	Token, Host, Socket, Phase, Scope string
	View                              string
	Tab                               string
	Board                             int64
	Header                            herdrmux.FleetPane
	Members                           []fleetMember
	Anchors                           []herdrmux.FleetPane
	Ratios                            []fleetRatio
}

func fleetHost() string {
	h, _ := os.Hostname()
	machine, _ := os.ReadFile("/etc/machine-id")
	sum := sha256.Sum256(append([]byte(h), machine...))
	return hex.EncodeToString(sum[:])
}
func (m *Manager) loadFleet(ctx context.Context) (*fleetJournal, error) {
	if m.Store == nil {
		return nil, nil
	}
	p, err := m.Store.FleetJournal(ctx)
	if err != nil || p == "" {
		return nil, err
	}
	var j fleetJournal
	err = json.Unmarshal([]byte(p), &j)
	if err == nil && (j.Token == "" || j.Header.Terminal == "") {
		err = errors.New("invalid Fleet recovery journal")
	}
	if j.Tab == "" {
		j.Tab = j.Header.Tab
	}
	return &j, err
}
func (m *Manager) saveFleet(ctx context.Context, j *fleetJournal, create bool) error {
	p, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return m.Store.SaveFleetJournal(ctx, string(p), create)
}
func (m *Manager) fleetPersist(ctx context.Context, j *fleetJournal, member *fleetMember, p herdrmux.FleetPane) error {
	member.Pane = p
	ref := member.Original
	ref.Namespace = p.Workspace
	if strings.Contains(ref.ID, ":p") {
		ref.ID = p.ID
	}
	meta := map[string]any{}
	_ = json.Unmarshal([]byte(ref.Metadata), &meta)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["pane_id"], meta["tab_id"], meta["terminal_id"] = p.ID, p.Tab, p.Terminal
	meta["server_socket"] = j.Socket
	raw, _ := json.Marshal(meta)
	ref.Metadata = string(raw)
	payload, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return m.Store.PersistFleetReference(ctx, member.Session, ref.Namespace, ref.ID, ref.Metadata, string(payload))
}
func fleetFind(panes []herdrmux.FleetPane, terminal string) (herdrmux.FleetPane, bool, error) {
	var found herdrmux.FleetPane
	count := 0
	for _, p := range panes {
		if p.Terminal == terminal {
			found = p
			count++
		}
	}
	if count > 1 {
		return found, false, errors.New("ambiguous Herdr terminal identity")
	}
	return found, count == 1, nil
}

func (m *Manager) fleetPlan(ctx context.Context, boardID int64, width, height int) (*fleetJournal, multiplexer.FleetSummary, error) {
	summary := multiplexer.FleetSummary{Scope: "all boards"}
	if m.Store == nil {
		return nil, summary, errors.New("Fleet storage is unavailable")
	}
	if boardID != 0 {
		b, err := m.Store.BoardByID(ctx, boardID)
		if err != nil {
			return nil, summary, err
		}
		summary.Scope = b.Name
	}
	existing, err := m.loadFleet(ctx)
	if err != nil {
		return nil, summary, err
	}
	if existing != nil {
		available, err := m.fleetOwnerAvailable()
		if err != nil {
			return existing, summary, err
		}
		if !available {
			return existing, summary, errors.New("Fleet is active in another Kanbi tab; return from its header first")
		}
		summary.Scope = existing.Scope
		summary.Count = len(existing.Members)
		summary.Recovery = true
		summary.View = existing.View
		return existing, summary, nil
	}
	if width < 40 || height < 14 {
		return nil, summary, errors.New("Fleet needs a larger terminal (at least 40×14)")
	}
	id := os.Getenv("HERDR_PANE_ID")
	if id == "" {
		return nil, summary, errors.New("Fleet requires Kanbi's own Herdr pane ID")
	}
	a := m.herdrAdapter()
	socket, err := a.FleetServer(ctx)
	if err != nil {
		return nil, summary, err
	}
	header, err := a.FleetPane(ctx, id)
	if err != nil {
		return nil, summary, err
	}
	layout, err := a.FleetLayout(ctx, id)
	if err != nil {
		return nil, summary, err
	}
	if layout.Zoomed || len(layout.Panes) != 1 {
		return nil, summary, errors.New("Fleet requires an unzoomed Kanbi tab with only its board pane")
	}
	width = min(width, layout.Area.Width-1)
	height = min(height, layout.Area.Height)
	focus, zoom, err := fleetBindings()
	if err != nil {
		return nil, summary, err
	}
	summary.FocusHint, summary.ZoomHint = focus, zoom
	j := &fleetJournal{Host: fleetHost(), Socket: socket, Phase: "entering", Scope: summary.Scope, Board: boardID, Header: header, Tab: header.Tab}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return nil, summary, err
	}
	j.Token = hex.EncodeToString(token)
	tickets, err := m.Store.ListTickets(ctx, false)
	if err != nil {
		return nil, summary, err
	}
	boards, err := m.Store.ListBoards(ctx)
	if err != nil {
		return nil, summary, err
	}
	included := map[int64]bool{}
	for _, b := range boards {
		included[b.ID] = true
	}
	seen := map[string]bool{}
	for _, t := range tickets {
		if !t.SessionActive || !included[t.BoardID] || (boardID != 0 && t.BoardID != boardID) {
			continue
		}
		if t.Runtime == kanban.StateStarting || t.Runtime == kanban.StateClosing {
			summary.Excluded++
			continue
		}
		ref := ContainerRefFromTicket(t)
		if ref.Kind != multiplexer.KindHerdr {
			summary.Excluded++
			continue
		}
		if other := fleetMetadataValue(ref.Metadata, "server_socket"); other != "" && other != socket {
			summary.Excluded++
			continue
		}
		paneID := herdrmux.FleetPaneID(ref.Metadata, ref.ID)
		if paneID == "" {
			return nil, summary, fmt.Errorf("%s has no validated pane reference", t.DisplayID)
		}
		p, err := a.FleetPane(ctx, paneID)
		if err != nil {
			return nil, summary, fmt.Errorf("validate %s before Fleet: %w", t.DisplayID, err)
		}
		if p.Workspace != ref.Namespace {
			return nil, summary, fmt.Errorf("%s namespace changed; reconcile before Fleet", t.DisplayID)
		}
		if expected := fleetMetadataTerminal(ref.Metadata); expected != "" && expected != p.Terminal {
			return nil, summary, fmt.Errorf("%s terminal identity changed", t.DisplayID)
		}
		valid, err := a.Validate(ctx, ref)
		if err != nil || !valid {
			if err == nil {
				err = errors.New("agent unavailable")
			}
			return nil, summary, fmt.Errorf("validate %s: %w", t.DisplayID, err)
		}
		workspaceLabel, err := a.FleetWorkspaceLabel(ctx, p.Workspace)
		if err != nil {
			return nil, summary, err
		}
		source, err := a.FleetLayout(ctx, p.ID)
		if err != nil {
			return nil, summary, err
		}
		if source.Zoomed || len(source.Panes) != 1 || p.Tab == header.Tab {
			return nil, summary, fmt.Errorf("%s needs a dedicated unzoomed source tab", t.DisplayID)
		}
		if seen[p.Terminal] {
			return nil, summary, errors.New("two sessions reference the same terminal")
		}
		seen[p.Terminal] = true
		j.Members = append(j.Members, fleetMember{Ticket: t.ID, Board: t.BoardID, Session: t.SessionID.Int64, Label: t.BoardName + " / " + t.DisplayID, WorkspaceLabel: workspaceLabel, OriginalTabID: p.Tab, Original: ref, Pane: p})
	}
	sort.Slice(j.Members, func(i, k int) bool {
		a, b := j.Members[i], j.Members[k]
		if a.Board != b.Board {
			return a.Board < b.Board
		}
		if a.Ticket != b.Ticket {
			return a.Ticket < b.Ticket
		}
		return a.Session < b.Session
	})
	n := len(j.Members)
	summary.Count = n
	if n == 0 {
		return nil, summary, errors.New("no active agents to gather")
	}
	if n > 6 {
		return nil, summary, fmt.Errorf("Fleet supports up to six agents; found %d (choose a named board)", n)
	}
	top, bottom := fleetRows(n, width)
	cols := top
	if bottom > cols {
		cols = bottom
	}
	rows := 1
	if bottom > 0 {
		rows = 2
	}
	// Allow one label row and Herdr's pane decorations as well as the header.
	minRows := (height-4)/rows - 4
	minCols := width/cols - 2
	if n == 3 && top == 2 && bottom == 1 {
		minRows = (height-4)/3 - 4
	}
	if minCols < 36 || minRows < 8 {
		return nil, summary, fmt.Errorf("%d Fleet agents need a larger terminal (each needs 36×8 content cells); current size %d×%d", n, width, height)
	}
	summary.Arrangement = fmt.Sprintf("%d columns", cols)
	if bottom > 0 {
		summary.Arrangement = fmt.Sprintf("%d + %d panes in two rows", top, bottom)
	}
	return j, summary, nil
}
func fleetMetadataTerminal(metadata string) string {
	return fleetMetadataValue(metadata, "terminal_id")
}
func fleetMetadataValue(metadata, key string) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(metadata), &m)
	v, _ := m[key].(string)
	return v
}
func fleetRows(n, width int) (int, int) {
	switch n {
	case 1, 2:
		return n, 0
	case 3:
		if width/3-2 < 36 {
			return 2, 1
		}
		return 3, 0
	case 4:
		return 2, 2
	case 5:
		return 3, 2
	default:
		return 3, 3
	}
}

func (m *Manager) PreviewFleet(ctx context.Context, boardID int64, width, height int) (multiplexer.FleetSummary, error) {
	ctx, unlock, err := m.lockRuntime(ctx)
	if err != nil {
		return multiplexer.FleetSummary{}, err
	}
	defer unlock()
	_, summary, err := m.fleetPlan(ctx, boardID, width, height)
	return summary, err
}

func (m *Manager) EnterFleet(ctx context.Context, boardID int64, width, height int, view string) error {
	ctx, unlock, err := m.lockRuntime(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	j, _, err := m.fleetPlan(ctx, boardID, width, height)
	if err != nil {
		return err
	}
	if old, err := m.loadFleet(ctx); err != nil {
		return err
	} else if old != nil {
		return errors.New("Fleet restoration pending; restore before gathering again")
	}
	if err = m.acquireFleetOwner(); err != nil {
		return err
	}
	j.View = view
	if err = m.saveFleet(ctx, j, true); err != nil {
		m.releaseFleetOwner()
		return err
	}
	a := m.herdrAdapter()
	create := func(target, direction string, ratio float64, label string) (herdrmux.FleetPane, error) {
		before, err := a.FleetPanes(ctx)
		if err != nil {
			return herdrmux.FleetPane{}, err
		}
		intent := &fleetAnchorIntent{Marker: j.Token + "-" + strconv.Itoa(len(j.Anchors))}
		for _, p := range before {
			intent.Before = append(intent.Before, p.Terminal)
			if p.ID == target {
				intent.TargetTab = p.Tab
			}
		}
		j.PendingAnchor = intent
		if err = m.saveFleet(ctx, j, false); err != nil {
			return herdrmux.FleetPane{}, err
		}
		p, err := a.FleetSplit(ctx, target, direction, ratio, intent.Marker)
		if err != nil {
			return p, err
		}
		j.Anchors = append(j.Anchors, p)
		j.PendingAnchor = nil
		if err = m.saveFleet(ctx, j, false); err != nil {
			return p, err
		}
		return p, a.FleetPark(ctx, p, j.Token, label)
	}
	build := func() error {
		for i := range j.Members {
			p, err := create(j.Members[i].Pane.ID, "down", .5, "Fleet: return from Kanbi header")
			if err != nil {
				return err
			}
			j.Members[i].Source = p
			if err = m.saveFleet(ctx, j, false); err != nil {
				return err
			}
		}
		first, err := create(j.Header.ID, "down", 4/float64(height), j.Members[0].Label)
		if err != nil {
			return err
		}
		j.Ratios = append(j.Ratios, fleetRatio{Path: []bool{}, Ratio: 4 / float64(height), Thin: true})
		top, bottom := fleetRows(len(j.Members), width)
		slots := make([]herdrmux.FleetPane, len(j.Members))
		slots[0] = first
		if bottom > 0 {
			p, err := create(first.ID, "down", float64(top)/float64(top+bottom), j.Members[top].Label)
			if err != nil {
				return err
			}
			slots[top] = p
			j.Ratios = append(j.Ratios, fleetRatio{Path: []bool{true}, Ratio: float64(top) / float64(top+bottom)})
		}
		for row, start := range []int{0, top} {
			count := top
			if row == 1 {
				count = bottom
			}
			if count == 0 {
				continue
			}
			path := []bool{true}
			if bottom > 0 {
				path = append(path, row == 1)
			}
			for col := 1; col < count; col++ {
				ratio := 1 / float64(count-col+1)
				p, err := create(slots[start+col-1].ID, "right", ratio, j.Members[start+col].Label)
				if err != nil {
					return err
				}
				slots[start+col] = p
				j.Ratios = append(j.Ratios, fleetRatio{Path: append([]bool(nil), path...), Ratio: ratio})
				path = append(path, true)
			}
		}
		for i := range j.Members {
			member := &j.Members[i]
			member.Slot = slots[i]
			if err = m.saveFleet(ctx, j, false); err != nil {
				return err
			}
			moved, err := a.FleetMove(ctx, member.Pane.ID, j.Tab, member.Slot.ID, "down", .1)
			if err != nil {
				return err
			}
			if moved.Terminal != member.Pane.Terminal {
				return errors.New("Herdr move changed terminal identity")
			}
			if err = m.fleetPersist(ctx, j, member, moved); err != nil {
				return err
			}
		}
		if err = m.fleetResizeLocked(ctx, j); err != nil {
			return err
		}
		if err = a.FleetFocus(ctx, j.Header.ID); err != nil {
			return err
		}
		j.Phase = "active"
		return m.saveFleet(ctx, j, false)
	}
	if err = build(); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		rollbackCtx = multiplexer.WithOperationLock(rollbackCtx, multiplexer.OperationLock(ctx))
		defer cancel()
		if restoreErr := m.restoreFleetLocked(rollbackCtx, j); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("Fleet rollback pending: %w", restoreErr))
		}
		return err
	}
	return nil
}

func (m *Manager) fleetResizeLocked(ctx context.Context, j *fleetJournal) error {
	a := m.herdrAdapter()
	layout, err := a.FleetLayout(ctx, j.Header.ID)
	if err != nil {
		return err
	}
	if layout.Zoomed {
		return nil
	}
	for _, r := range j.Ratios {
		ratio := r.Ratio
		if r.Thin {
			ratio = 4 / float64(layout.Area.Height)
		}
		if err = a.FleetRatio(ctx, j.Socket, j.Tab, r.Path, ratio); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) ResizeFleet(ctx context.Context) error {
	ctx, unlock, err := m.lockRuntime(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	j, err := m.loadFleet(ctx)
	if err != nil || j == nil {
		return err
	}
	if !m.hasFleetOwner() || j.Phase != "active" {
		return nil
	}
	return m.fleetResizeLocked(ctx, j)
}
func (m *Manager) RestoreFleet(ctx context.Context) error {
	ctx, unlock, err := m.lockRuntime(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	j, err := m.loadFleet(ctx)
	if err != nil || j == nil {
		return err
	}
	if err = m.acquireFleetOwner(); err != nil {
		return err
	}
	return m.restoreFleetLocked(ctx, j)
}
func (m *Manager) restoreFleetLocked(ctx context.Context, j *fleetJournal) error {
	a := m.herdrAdapter()
	socket, err := a.FleetServer(ctx)
	if err != nil {
		return err
	}
	if j.Host != fleetHost() || socket != j.Socket {
		return errors.New("Fleet journal belongs to another machine or Herdr server; no panes were moved")
	}
	if j.PendingAnchor != nil {
		if err = m.resolveFleetAnchor(ctx, j); err != nil {
			return err
		}
	}
	panes, err := a.FleetPanes(ctx)
	if err != nil {
		return err
	}
	header, found, err := fleetFind(panes, j.Header.Terminal)
	if err != nil {
		return err
	}
	if found {
		j.Header = header
		if err = a.FleetUnzoom(ctx, header.ID); err != nil {
			return err
		}
	}
	j.Phase = "restoring"
	if err = m.saveFleet(ctx, j, false); err != nil {
		return err
	}
	for i := range j.Members {
		member := &j.Members[i]
		if member.Restored {
			continue
		}
		panes, err = a.FleetPanes(ctx)
		if err != nil {
			return err
		}
		p, live, err := fleetFind(panes, member.Pane.Terminal)
		if err != nil {
			return err
		}
		if live {
			// A return may have completed before the journal update. Original tabs
			// remain alive through their anchors, making this check idempotent.
			if member.RestoreNew && p.Tab != j.Tab {
				member.Restored = true
				if err = m.fleetRestoreName(ctx, *member, p); err != nil {
					return err
				}
				if err = m.fleetPersist(ctx, j, member, p); err != nil {
					return err
				}
				continue
			}
			if p.Tab != member.OriginalTab() {
				source, found, err := fleetFind(panes, member.Source.Terminal)
				if err != nil {
					return err
				}
				if !found {
					if p.Tab != j.Tab {
						return fmt.Errorf("%s moved externally; refusing to move it again", member.Label)
					}
					workspace := ""
					// A live restored terminal proves the replacement workspace identity.
					for _, prior := range j.Members {
						if prior.Restored && prior.Original.Namespace == member.Original.Namespace {
							if live, ok, _ := fleetFind(panes, prior.Pane.Terminal); ok {
								workspace = live.Workspace
								break
							}
						}
					}
					member.RestoreNew = true
					if err = m.saveFleet(ctx, j, false); err != nil {
						return err
					}
					moved, err := a.FleetMoveNew(ctx, p.ID, workspace, member.WorkspaceLabel, member.Original.Name)
					if err != nil {
						return err
					}
					if moved.Terminal != member.Pane.Terminal {
						return errors.New("recreated source changed terminal identity")
					}
					member.Restored = true
					if err = m.fleetRestoreName(ctx, *member, moved); err != nil {
						return err
					}
					if err = m.fleetPersist(ctx, j, member, moved); err != nil {
						return err
					}
					continue
				}
				if source.Tab != member.OriginalTab() || source.Workspace != member.Original.Namespace {
					return fmt.Errorf("source anchor moved externally for %s", member.Label)
				}
				if p.Tab != j.Tab {
					return fmt.Errorf("%s moved externally; refusing to move it again", member.Label)
				}
				p, err = a.FleetMove(ctx, p.ID, source.Tab, source.ID, "down", .1)
				if err != nil {
					return err
				}
				if p.Terminal != member.Pane.Terminal {
					return errors.New("restored terminal identity mismatch")
				}
			}
			if err = m.fleetRestoreName(ctx, *member, p); err != nil {
				return err
			}
			if err = m.fleetPersist(ctx, j, member, p); err != nil {
				return err
			}
		}
		if !live {
			ses, active, err := m.Store.ActiveSession(ctx, member.Ticket)
			if err != nil {
				return err
			}
			if active && ses.ID == member.Session {
				if err = m.Store.MarkObservedSessionMissing(ctx, ses); err != nil {
					return err
				}
			}
		}
		member.Restored = true
		if err = m.saveFleet(ctx, j, false); err != nil {
			return err
		}
	}
	// Only close exact owned anchor terminals, never reused public IDs. If an
	// unrelated pane shares a source tab, removing our anchor leaves it untouched.
	for _, anchor := range j.Anchors {
		panes, err = a.FleetPanes(ctx)
		if err != nil {
			return err
		}
		p, found, err := fleetFind(panes, anchor.Terminal)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		if err = a.FleetClose(ctx, p.ID); err != nil {
			return err
		}
	}
	if found {
		_ = a.FleetFocus(ctx, header.ID)
	}
	if err = m.Store.DeleteFleetJournal(ctx); err != nil {
		return err
	}
	m.releaseFleetOwner()
	return nil
}
func (member fleetMember) OriginalTab() string {
	if member.OriginalTabID != "" {
		return member.OriginalTabID
	}
	var meta map[string]any
	_ = json.Unmarshal([]byte(member.Original.Metadata), &meta)
	if t, ok := meta["tab_id"].(string); ok && t != "" {
		return t
	}
	return member.Source.Tab
}

// Resolve configured native focus/zoom controls without editing user config.
// Herdr's default prefix+Tab visits every pane, including the header.
func fleetBindings() (string, string, error) {
	configPath := os.Getenv("HERDR_CONFIG_PATH")
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		configPath = filepath.Join(base, "herdr", "config.toml")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	values := map[string]string{"prefix": "ctrl+b", "cycle_pane_next": "prefix+tab", "zoom": "prefix+z", "fullscreen": ""}
	explicit := map[string]bool{}
	section := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			if end := strings.Index(line, "]"); end >= 0 {
				section = strings.TrimSpace(line[:end+1])
			} else {
				return "", "", errors.New("invalid Herdr keys section")
			}
			continue
		}
		if section != "[keys]" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), "\"'")
		if _, ok := values[key]; !ok {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) < 2 || (val[0] != '"' && val[0] != '\'') {
			return "", "", errors.New("Fleet could not read Herdr focus/zoom bindings")
		}
		quote := val[0]
		end := 1
		for end < len(val) {
			if val[end] == quote {
				break
			}
			if quote == '"' && val[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(val) {
			return "", "", errors.New("invalid Herdr key binding")
		}
		parsed := val[1:end]
		if quote == '"' {
			var err error
			parsed, err = strconv.Unquote(val[:end+1])
			if err != nil {
				return "", "", err
			}
		}
		trailing := strings.TrimSpace(val[end+1:])
		if trailing != "" && !strings.HasPrefix(trailing, "#") {
			return "", "", errors.New("invalid Herdr key binding suffix")
		}
		values[key] = parsed
		explicit[key] = true
	}
	if explicit["fullscreen"] && !explicit["zoom"] {
		values["zoom"] = values["fullscreen"]
	}
	if values["cycle_pane_next"] == "" || values["zoom"] == "" {
		return "", "", errors.New("Fleet needs native Herdr cycle_pane_next and zoom bindings")
	}
	if values["prefix"] == "" && (strings.Contains(values["cycle_pane_next"], "prefix+") || strings.Contains(values["zoom"], "prefix+")) {
		return "", "", errors.New("Fleet's native focus/zoom bindings require an enabled Herdr prefix")
	}

	resolve := func(v string) string { return strings.ReplaceAll(v, "prefix+", values["prefix"]+" then ") }
	return resolve(values["cycle_pane_next"]), resolve(values["zoom"]), nil
}

func (m *Manager) FleetStatus(ctx context.Context) (multiplexer.FleetStatus, error) {
	ctx, unlock, err := m.lockRuntime(ctx)
	if err != nil {
		return multiplexer.FleetStatus{}, err
	}
	defer unlock()
	j, err := m.loadFleet(ctx)
	if err != nil || j == nil {
		return multiplexer.FleetStatus{}, err
	}
	var observationErr error
	if j.Phase == "active" {
		observeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		observationErr = m.RefreshRuntime(observeCtx)
		cancel()
	}
	available, err := m.fleetOwnerAvailable()
	if err != nil {
		return multiplexer.FleetStatus{}, err
	}
	status := multiplexer.FleetStatus{Present: true, Recovery: available && (!m.hasFleetOwner() || j.Phase != "active"), Scope: j.Scope, Count: len(j.Members)}
	if layout, e := m.herdrAdapter().FleetLayout(ctx, j.Header.ID); e == nil {
		status.Width, status.Height = layout.Area.Width-1, layout.Area.Height
	}
	tickets, err := m.Store.ListTickets(ctx, false)
	if err != nil {
		return status, err
	}
	boards, err := m.Store.ListBoards(ctx)
	if err != nil {
		return status, err
	}
	included := map[int64]bool{}
	for _, b := range boards {
		included[b.ID] = true
	}
	members := map[int64]bool{}
	for _, member := range j.Members {
		members[member.Session] = true
	}
	for _, t := range tickets {
		if !t.SessionActive || !included[t.BoardID] || ContainerRefFromTicket(t).Kind != multiplexer.KindHerdr {
			continue
		}
		if socket := fleetMetadataValue(t.MuxMetadata.String, "server_socket"); socket != "" && socket != j.Socket {
			continue
		}
		if members[t.SessionID.Int64] {
			status.Live++
			if t.Runtime == kanban.StateWaitingForUser || t.Runtime == kanban.StateNeedsPermission {
				status.Attention++
			}
		} else if (j.Board == 0 || j.Board == t.BoardID) && t.Runtime != kanban.StateStarting && t.Runtime != kanban.StateClosing {
			status.New++
		}
	}
	return status, observationErr
}

func (m *Manager) resolveFleetAnchor(ctx context.Context, j *fleetJournal) error {
	intent := j.PendingAnchor
	if intent == nil {
		return nil
	}
	a := m.herdrAdapter()
	panes, err := a.FleetPanes(ctx)
	if err != nil {
		return err
	}
	old := map[string]bool{}
	for _, id := range intent.Before {
		old[id] = true
	}
	var owned []herdrmux.FleetPane
	unidentified := 0
	for _, p := range panes {
		if old[p.Terminal] {
			continue
		}
		if p.Tab == intent.TargetTab {
			unidentified++
		}
		matched, err := a.FleetAnchorOwned(ctx, p, intent.Marker)
		if err != nil {
			return err
		}
		if matched {
			owned = append(owned, p)
		}
	}
	if len(owned) == 0 && unidentified > 0 {
		return errors.New("pending Fleet split has an unverified terminal in its target tab; recovery journal retained")
	}
	if len(owned) > 1 {
		return errors.New("ambiguous Fleet anchor creation nonce; recovery journal retained")
	}
	if len(owned) == 1 {
		j.Anchors = append(j.Anchors, owned[0])
	}
	j.PendingAnchor = nil
	return m.saveFleet(ctx, j, false)
}

func (m *Manager) fleetRestoreName(ctx context.Context, member fleetMember, p herdrmux.FleetPane) error {
	ses, found, err := m.Store.LatestSession(ctx, member.Ticket)
	if err != nil {
		return err
	}
	if !found || ses.ID != member.Session || !ses.MuxContainerName.Valid || ses.MuxContainerName.String == member.Original.Name {
		return nil
	}
	raw, _ := json.Marshal(map[string]string{"pane_id": p.ID, "tab_id": p.Tab, "terminal_id": p.Terminal})
	return m.herdrAdapter().Rename(ctx, multiplexer.ContainerRef{Metadata: string(raw)}, ses.MuxContainerName.String)
}
