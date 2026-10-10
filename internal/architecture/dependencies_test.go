package architecture

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestLayerDependencies protects the inward-facing package boundaries that
// keep presentation and runtime adapters out of application policy.
func TestLayerDependencies(t *testing.T) {
	root := repositoryRoot(t)
	rules := map[string][]string{
		"internal/app":     {"github.com/carlotran4/kanbi/internal/tui", "github.com/carlotran4/kanbi/internal/runtime", "github.com/carlotran4/kanbi/internal/multiplexer/herdr"},
		"internal/session": {"github.com/carlotran4/kanbi/internal/app", "github.com/carlotran4/kanbi/internal/tui", "github.com/carlotran4/kanbi/internal/runtime", "github.com/carlotran4/kanbi/internal/multiplexer/herdr"},
		"internal/storage": {"github.com/carlotran4/kanbi/internal/app", "github.com/carlotran4/kanbi/internal/session", "github.com/carlotran4/kanbi/internal/ticketbackend", "github.com/carlotran4/kanbi/internal/runtime", "github.com/carlotran4/kanbi/internal/tui"},
		"internal/tui":     {"github.com/carlotran4/kanbi/internal/runtime", "github.com/carlotran4/kanbi/internal/multiplexer/herdr"},
	}

	for dir, forbidden := range rules {
		dir, forbidden := dir, forbidden
		t.Run(strings.ReplaceAll(dir, "/", "_"), func(t *testing.T) {
			files, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
			if err != nil {
				t.Fatal(err)
			}
			for _, filename := range files {
				if strings.HasSuffix(filename, "_test.go") {
					continue
				}
				file, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.ImportsOnly)
				if err != nil {
					t.Fatalf("parse %s: %v", filename, err)
				}
				for _, spec := range file.Imports {
					path, err := strconv.Unquote(spec.Path.Value)
					if err != nil {
						t.Fatalf("unquote import in %s: %v", filename, err)
					}
					for _, blocked := range forbidden {
						if path == blocked || strings.HasPrefix(path, blocked+"/") {
							t.Errorf("%s imports forbidden outward dependency %s", filepath.Base(filename), path)
						}
					}
				}
			}
		})
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate dependency test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
