package buildinfo

import "testing"

func TestCurrentUsesInjectedMetadataAndNormalizesVersion(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = oldVersion, oldCommit, oldDate })
	Version, Commit, BuildDate = "v1.2.3", "abc123", "2026-07-11T12:00:00Z"
	got := Current(3)
	if got.Version != "1.2.3" || got.Commit != "abc123" || got.BuildDate != BuildDate || got.Schema != 3 || got.GoVersion == "" || got.OS == "" || got.Arch == "" {
		t.Fatalf("Current()=%+v", got)
	}
}

func TestCurrentHasDevelopmentFallbacks(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = oldVersion, oldCommit, oldDate })
	Version, Commit, BuildDate = "", "", ""
	got := Current(3)
	if got.Version == "" || got.Commit == "" || got.BuildDate == "" {
		t.Fatalf("missing fallback: %+v", got)
	}
}
