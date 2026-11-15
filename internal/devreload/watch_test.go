package devreload

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0755); err != nil {
		t.Fatal(err)
	}
}
func source(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"cmd", "internal"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, "go.mod"), "module fixture")
	write(t, filepath.Join(root, "go.sum"), "")
	return root
}
func TestFingerprintChanges(t *testing.T) {
	root := source(t)
	path := filepath.Join(root, "internal", "embed.ts")
	initial, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, "one")
	added, _ := Fingerprint(root)
	info, _ := os.Stat(path)
	write(t, path, "two")
	os.Chtimes(path, info.ModTime(), info.ModTime())
	edited, _ := Fingerprint(root)
	if initial == added || added == edited {
		t.Fatal("missed source/embedded asset change")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	deleted, _ := Fingerprint(root)
	if deleted != initial {
		t.Fatal("deletion not captured")
	}
	write(t, filepath.Join(root, "go.mod"), "module changed")
	module, _ := Fingerprint(root)
	if module == initial {
		t.Fatal("missed module change")
	}
}
func TestBuildAtomicReplacementAndFailure(t *testing.T) {
	root := source(t)
	target := filepath.Join(root, "kanbi")
	write(t, target, "old")
	bin := t.TempDir()
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	compiler := filepath.Join(bin, "go")
	write(t, compiler, "#!/bin/sh\necho compiler-error >&2\nexit 1\n")
	result := build(context.Background(), root, target)
	content, _ := os.ReadFile(target)
	log, _ := os.ReadFile(result.Log)
	if result.Err == nil || string(content) != "old" || string(log) != "compiler-error\n" {
		t.Fatalf("failure replaced executable: %+v", result)
	}
	write(t, compiler, "#!/bin/sh\nprintf new > \"$4\"\n")
	result = build(context.Background(), root, target)
	content, _ = os.ReadFile(target)
	if result.Err != nil || result.Path != target || string(content) != "new" {
		t.Fatalf("replacement failed: %+v content=%s", result, content)
	}
	entries, _ := filepath.Glob(filepath.Join(root, "reload-build-*"))
	if len(entries) != 0 {
		t.Fatal("build scratch leaked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result = build(ctx, root, target)
	content, _ = os.ReadFile(target)
	if result.Err == nil || string(content) != "new" {
		t.Fatal("cancelled build replaced target")
	}
}

func TestWatchDebouncesAndStopsWithBoard(t *testing.T) {
	root := source(t)
	target := filepath.Join(root, "kanbi")
	write(t, target, "old")
	bin := t.TempDir()
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	write(t, filepath.Join(bin, "go"), "#!/bin/sh\nprintf new > \"$4\"\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan Result, 8)
	done := make(chan struct{})
	go func() { defer close(done); Watch(ctx, root, target, func(result Result) { results <- result }) }()
	// Wait until Watch has observed the initial source, then save a burst.
	time.Sleep(100 * time.Millisecond)
	path := filepath.Join(root, "internal", "changed.go")
	write(t, path, "package changed\n")
	write(t, path, "package changed\n// final save\n")
	select {
	case result := <-results:
		if result.Err != nil || result.Path != target {
			t.Fatalf("watch build: %+v", result)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("watch did not build changed sources")
	}
	select {
	case extra := <-results:
		t.Fatalf("duplicate build: %+v", extra)
	case <-time.After(time.Second):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watch survived board cancellation")
	}
}
