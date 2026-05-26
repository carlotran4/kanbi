package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"agent-kanban/internal/config"
	"agent-kanban/internal/tmux"
)

type doctorSeverity string

const (
	doctorOK    doctorSeverity = "ok"
	doctorWarn  doctorSeverity = "warn"
	doctorFatal doctorSeverity = "fatal"
)

type doctorResult struct {
	Severity doctorSeverity
	Name     string
	Detail   string
	Err      error
}

type doctorReport struct {
	Results []doctorResult
}

func (r doctorReport) FatalErr() error {
	for _, result := range r.Results {
		if result.Severity == doctorFatal {
			if result.Err != nil {
				return result.Err
			}
			if result.Detail != "" {
				return fmt.Errorf("%s: %s", result.Name, result.Detail)
			}
			return fmt.Errorf("%s failed", result.Name)
		}
	}
	return nil
}

type doctorProber struct {
	lookPath          func(string) (string, error)
	commandOutput     func(string, ...string) ([]byte, error)
	openStore         func(context.Context, config.Config) (io.Closer, error)
	ensureDirs        func(config.Config) error
	insideTmux        func() bool
	getenv            func(string) string
	ensureTmuxSession func(context.Context, config.Config) error
}

func defaultDoctorProber() doctorProber {
	return doctorProber{
		lookPath:      exec.LookPath,
		commandOutput: func(name string, args ...string) ([]byte, error) { return exec.Command(name, args...).CombinedOutput() },
		openStore: func(ctx context.Context, cfg config.Config) (io.Closer, error) {
			return openStore(ctx, cfg)
		},
		ensureDirs: config.Config.EnsureDirs,
		insideTmux: tmux.InsideTmux,
		getenv:     os.Getenv,
		ensureTmuxSession: func(ctx context.Context, cfg config.Config) error {
			cli, err := newCLIContext(ctx, cfg)
			if err != nil {
				return err
			}
			defer cli.Close()
			return cli.Manager().EnsureSession(ctx)
		},
	}
}

func probeDoctor(ctx context.Context, cfg config.Config, prober doctorProber) doctorReport {
	if prober.lookPath == nil {
		prober.lookPath = exec.LookPath
	}
	if prober.commandOutput == nil {
		prober.commandOutput = func(name string, args ...string) ([]byte, error) { return exec.Command(name, args...).CombinedOutput() }
	}
	if prober.openStore == nil {
		prober.openStore = func(ctx context.Context, cfg config.Config) (io.Closer, error) { return openStore(ctx, cfg) }
	}
	if prober.ensureDirs == nil {
		prober.ensureDirs = config.Config.EnsureDirs
	}
	if prober.insideTmux == nil {
		prober.insideTmux = tmux.InsideTmux
	}
	if prober.getenv == nil {
		prober.getenv = os.Getenv
	}
	if prober.ensureTmuxSession == nil {
		prober.ensureTmuxSession = func(ctx context.Context, cfg config.Config) error {
			cli, err := newCLIContext(ctx, cfg)
			if err != nil {
				return err
			}
			defer cli.Close()
			return cli.Manager().EnsureSession(ctx)
		}
	}

	var results []doctorResult
	add := func(severity doctorSeverity, name, detail string, err error) {
		results = append(results, doctorResult{Severity: severity, Name: name, Detail: detail, Err: err})
	}

	tmuxPath, err := prober.lookPath("tmux")
	if err != nil {
		add(doctorFatal, "tmux", "is required", fmt.Errorf("tmux is required: %w", err))
		return doctorReport{Results: results}
	}
	if out, err := prober.commandOutput(tmuxPath, "-V"); err == nil {
		add(doctorOK, "tmux", strings.TrimSpace(string(out)), nil)
	} else {
		add(doctorOK, "tmux", "", nil)
	}

	store, err := prober.openStore(ctx, cfg)
	if err != nil {
		add(doctorFatal, "sqlite", cfg.DBPath, err)
		return doctorReport{Results: results}
	}
	_ = store.Close()
	add(doctorOK, "sqlite", cfg.DBPath, nil)

	if err := prober.ensureDirs(cfg); err != nil {
		add(doctorFatal, "config", cfg.Paths.ConfigFile, err)
		return doctorReport{Results: results}
	}
	add(doctorOK, "config", cfg.Paths.ConfigFile, nil)

	if shell := prober.getenv("SHELL"); shell != "" {
		add(doctorOK, "shell", shell, nil)
	} else {
		add(doctorWarn, "shell", "not detected", nil)
	}
	if prober.insideTmux() {
		add(doctorOK, "inside tmux", "", nil)
	} else {
		add(doctorWarn, "not inside tmux", "", nil)
	}
	if term := prober.getenv("TERM"); term != "" {
		add(doctorOK, "terminal", term, nil)
	} else {
		add(doctorWarn, "terminal", "unknown", nil)
	}

	if err := prober.ensureTmuxSession(ctx, cfg); err != nil {
		add(doctorFatal, "tmux session", cfg.TmuxSession, fmt.Errorf("tmux session unusable: %w", err))
		return doctorReport{Results: results}
	}
	add(doctorOK, "tmux session", cfg.TmuxSession, nil)

	for _, name := range sortedHarnessNames(cfg.Harnesses) {
		h := cfg.Harnesses[name]
		if len(h.Start) == 0 {
			add(doctorWarn, "harness "+name, "has no start command", nil)
			continue
		}
		if _, err := prober.lookPath(h.Start[0]); err != nil {
			add(doctorWarn, "harness "+name, "not found: "+h.Start[0], nil)
		} else {
			add(doctorOK, "harness", name, nil)
		}
	}

	return doctorReport{Results: results}
}

func sortedHarnessNames(harnesses map[string]config.Harness) []string {
	names := make([]string, 0, len(harnesses))
	for name := range harnesses {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func printDoctorReport(w io.Writer, report doctorReport) {
	fmt.Fprintln(w, "agent-kanban doctor")
	for _, result := range report.Results {
		if result.Severity == doctorFatal {
			continue
		}
		if result.Detail == "" {
			fmt.Fprintln(w, result.Severity, result.Name)
		} else {
			fmt.Fprintln(w, result.Severity, result.Name, result.Detail)
		}
	}
}
