package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// These values are overridden in release builds with -ldflags -X.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Schema    int    `json:"schema_version"`
}

func Current(schema int) Info {
	version, commit, date := strings.TrimSpace(Version), strings.TrimSpace(Commit), strings.TrimSpace(BuildDate)
	if version == "" {
		version = "dev"
	}
	if commit == "" {
		commit = "unknown"
	}
	if date == "" {
		date = "unknown"
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			version = strings.TrimPrefix(bi.Main.Version, "v")
		}
		for _, setting := range bi.Settings {
			switch setting.Key {
			case "vcs.revision":
				if commit == "unknown" && setting.Value != "" {
					commit = setting.Value
				}
			case "vcs.time":
				if date == "unknown" && setting.Value != "" {
					if parsed, err := time.Parse(time.RFC3339, setting.Value); err == nil {
						date = parsed.UTC().Format(time.RFC3339)
					} else {
						date = setting.Value
					}
				}
			}
		}
	}
	return Info{Version: strings.TrimPrefix(version, "v"), Commit: commit, BuildDate: date, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, Schema: schema}
}
