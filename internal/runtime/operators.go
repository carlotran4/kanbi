package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
	"github.com/robfig/cron/v3"
)

// ParseOperatorSchedule accepts fixed-clock five-field cron and common presets.
// @every is deliberately excluded: it drifts from wall-clock schedules.
func NextOperatorWake(spec, zone string, after time.Time) (*time.Time, error) {
	if spec == "" {
		return nil, nil
	}
	if zone == "" {
		return nil, errors.New("a schedule requires an explicit timezone")
	}
	if _, e := time.LoadLocation(zone); e != nil {
		return nil, e
	}
	presets := map[string]string{"15m": "*/15 * * * *", "hourly": "0 * * * *", "daily": "0 9 * * *"}
	if p, ok := presets[spec]; ok {
		spec = p
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, e := parser.Parse("CRON_TZ=" + zone + " " + spec)
	if e != nil {
		return nil, e
	}
	next := schedule.Next(after)
	if next.IsZero() {
		return nil, errors.New("schedule has no occurrence within five years")
	}
	next = next.UTC()
	return &next, nil
}
func (m *Manager) OperatorInstructionsPath(id int64) string {
	root := filepath.Dir(m.Config.Paths.ConfigFile)
	absolute, _ := filepath.Abs(filepath.Join(root, "operators", strconv.FormatInt(id, 10), "instructions.md"))
	return absolute
}
func (m *Manager) ReadOperatorInstructions(id int64) (string, error) {
	b, e := os.ReadFile(m.OperatorInstructionsPath(id))
	return string(b), e
}
func (m *Manager) WriteOperatorInstructions(id int64, instructions string) error {
	path := m.OperatorInstructionsPath(id)
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	file, e := os.CreateTemp(filepath.Dir(path), ".instructions-")
	if e != nil {
		return e
	}
	defer os.Remove(file.Name())
	if _, e = file.WriteString(instructions); e != nil {
		file.Close()
		return e
	}
	if e = file.Close(); e != nil {
		return e
	}
	return os.Rename(file.Name(), path)
}
func (m *Manager) SaveOperator(ctx context.Context, o storage.Operator, instructions string) (storage.Operator, error) {
	if strings.TrimSpace(o.Name) == "" {
		return o, errors.New("name is required")
	}
	cwd, e := filepath.Abs(o.CWD)
	if e != nil {
		return o, e
	}
	info, e := os.Stat(cwd)
	if e != nil {
		return o, e
	}
	if !info.IsDir() {
		return o, errors.New("working directory must be a directory")
	}
	o.CWD = cwd
	o.NextWake, e = NextOperatorWake(o.Cron, o.Timezone, time.Now())
	if e != nil {
		return o, e
	}
	if o.ID == 0 {
		o, e = m.Store.CreateOperator(ctx, o)
	} else {
		old, err := m.Store.Operator(ctx, o.ID)
		if err != nil {
			return o, err
		}
		if old.Harness != o.Harness {
			return o, errors.New("operator harness is immutable; delete and recreate to change it")
		}
		e = m.Store.UpdateOperator(ctx, o)
	}
	if e != nil {
		return o, e
	}
	return o, m.WriteOperatorInstructions(o.ID, instructions)
}
func (m *Manager) PauseOperator(ctx context.Context, id int64, paused bool) error {
	o, e := m.Store.Operator(ctx, id)
	if e != nil {
		return e
	}
	next, e := NextOperatorWake(o.Cron, o.Timezone, time.Now())
	if e != nil {
		return e
	}
	return m.Store.PauseOperator(ctx, id, paused, next)
}
func (m *Manager) OpenOperator(ctx context.Context, id int64, fresh bool) error {
	t, e := m.Store.OperatorTicket(ctx, id)
	if e != nil {
		return e
	}
	if fresh && t.SessionActive {
		return errors.New("close the active Operator session explicitly before starting fresh")
	}
	// Initialization is ordinary harness context. Every later activation explicitly
	// names this same file, including after an explicit conversation reset.
	t.Body = fmt.Sprintf("You are the workspace-wide Operator %q. Your authoritative standing instructions live at %s. Read that file before acting and again on every wake. Kanbi will deliver wake reasons and pending work through messages inbox. Normal harness tools and permissions apply. Do not start a responsibility turn until a wake arrives.", t.Title, m.OperatorInstructionsPath(id))
	var result error
	if fresh {
		result = m.lifecycle().launchFreshOperator(ctx, t)
	} else {
		result = m.OpenTicket(ctx, t, !t.SessionID.Valid)
	}
	if result != nil {
		return result
	}
	return m.Store.SetOperatorError(ctx, id, "")
}

// Use the existing policy, ref capture, launch failure handling and runtime lock.
func (l ticketLifecycle) launchFreshOperator(ctx context.Context, t storage.Ticket) error {
	return l.Execute(ctx, lifecycleRequest{Ticket: t, StartFresh: true, SendPrompt: true})
}
func (m *Manager) RunOperator(ctx context.Context, id int64, prompt string) error {
	_, e := m.Store.QueueOperatorWake(ctx, id, "run now", prompt, time.Now())
	return e
}
func (m *Manager) StopOperator(ctx context.Context, id int64) error {
	ctx, unlock, e := m.lockRuntime(ctx)
	if e != nil {
		return e
	}
	defer unlock()
	ses, ok, e := m.Store.ActiveSession(ctx, -id)
	if e != nil || !ok {
		return e
	}
	ref := ContainerRefFromSession(ses)
	a, e := m.multiplexerAdapter(ref.Kind)
	if e != nil {
		return e
	}
	valid, e := a.Validate(ctx, ref)
	if e != nil {
		return e
	}
	if !valid {
		return errors.New("operator terminal is missing")
	}
	return a.SendKeys(ctx, ref, "C-c")
}

// Called only by Kanbi's existing runtime refresh. No detached timers or service.
// Runtime lock is reentrant through context and fences launch/delivery against
// other processes, close, Fleet and explicit resets.
func (m *Manager) RefreshOperators(ctx context.Context) error {
	ctx, unlock, err := m.lockRuntime(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err = m.runtimeReady(ctx); err != nil {
		return err
	}
	operators, e := m.Store.Operators(ctx)
	if e != nil {
		return e
	}
	if len(operators) == 0 {
		return nil
	}
	if e = m.Store.ReconcileOperatorReceipts(ctx); e != nil {
		return e
	}
	if e = m.Store.RegisterOperatorRunner(ctx, m.operatorRunnerKey(), os.Getpid()); e != nil {
		return e
	}
	now := time.Now().UTC()
	for _, o := range operators {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t, e := m.Store.OperatorTicket(ctx, o.ID)
		if e != nil {
			return e
		}
		if t.SessionActive && t.Runtime == "starting" {
			reason := "interrupted Operator launch; inspect any existing Herdr tab before retry or start fresh"
			if e = m.Store.FailSessionLaunch(ctx, t.SessionID.Int64, reason); e != nil {
				return e
			}
			if e = m.Store.SetOperatorError(ctx, o.ID, reason); e != nil {
				return e
			}
			continue
		}
		if !o.SchedulePaused && o.NextWake != nil && !o.NextWake.After(now) {
			next, e := NextOperatorWake(o.Cron, o.Timezone, now)
			if e != nil {
				return e
			}
			if next != nil {
				if e = m.Store.MaterializeRecurring(ctx, o, now, *next); e != nil {
					return e
				}
			}
		}
		wakes, e := m.Store.OperatorWakeups(ctx, o.ID)
		if e != nil {
			return e
		}
		due := false
		for _, w := range wakes {
			if w.State == "pending" && !w.DueAt.After(now) && (!o.SchedulePaused || w.Reason == "run now") {
				due = true
			}
		}
		mail, e := m.Store.SessionInbox(ctx, -o.ID, false)
		if e != nil {
			return e
		}
		for _, msg := range mail {
			if msg.State == "queued" && (msg.Sender != 0 || !o.SchedulePaused || strings.Contains(msg.Body, "Wake reason: run now")) {
				due = true
			}
		}
		if !due || o.LastError != "" {
			continue
		}
		if !t.SessionActive {
			if e = m.OpenOperator(ctx, o.ID, false); e != nil {
				_ = m.Store.SetOperatorError(ctx, o.ID, e.Error())
				continue
			}
		}
		for _, w := range wakes {
			if w.State != "pending" || w.DueAt.After(now) || (o.SchedulePaused && w.Reason != "run now") {
				continue
			}
			if e = m.Store.EnqueueOperatorWakeMessage(ctx, w); e != nil {
				return e
			}
		}
	}
	return nil
}

func (m *Manager) OperatorMessageNotice(id int64, message storage.SessionMessage) string {
	reason := "incoming agent message"
	if message.Sender == 0 {
		reason = message.Body
	}
	if message.Sender != 0 {
		reason = "Wake reason: " + reason
	}
	return fmt.Sprintf("Read %q before acting.\n\n%s\n\nRead kanbi messages inbox for all pending work, preserve distinct follow-up prompts, and acknowledge messages you take responsibility for. Reply to incoming agents using kanbi messages reply ID --key STABLE_KEY --body TEXT. Normal harness permissions apply. Message ID: %d.", m.OperatorInstructionsPath(id), reason, message.ID)
}

type operatorWakeContextKey struct{}

// StartOperators starts one in-process worker owned by the board's Manager.
// Close cancels and joins it; CLI invocations never implicitly start scheduling.
func (m *Manager) StartOperators() {
	b := m.background
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.closed || b.operatorsStarted {
		b.mu.Unlock()
		return
	}
	b.operatorsStarted = true
	b.wg.Add(1)
	b.mu.Unlock()
	go func() {
		defer b.wg.Done()
		defer m.Store.RemoveOperatorRunner(context.Background(), m.operatorRunnerKey())
		for {
			ctx, cancel := context.WithTimeout(b.ctx, 60*time.Second)
			ctx = context.WithValue(ctx, operatorWakeContextKey{}, true)
			locked, unlock, e := m.lockRuntime(ctx)
			if e == nil {
				if e = m.runtimeReady(locked); e == nil {
					e = m.RefreshOperators(locked)
				}
				unlock()
			}
			cancel()
			if e != nil && b.ctx.Err() == nil {
				m.reportBackgroundError(e)
			}
			select {
			case <-b.ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
}

func (m *Manager) operatorRunnerKey() string { return fmt.Sprintf("%d:%p", os.Getpid(), m) }
