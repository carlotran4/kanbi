package multiplexer

import (
	"context"
	"errors"
	"time"
)

// ErrContainerNotFound means the multiplexer authoritatively reported that a
// durable container reference no longer exists.
var ErrContainerNotFound = errors.New("multiplexer container not found")

// Kind identifies the runtime multiplexer provider that owns a live container.
type Kind string

const (
	KindTmux  Kind = "tmux"
	KindHerdr Kind = "herdr"
)

// DetectionSource describes where runtime state came from. Native provider state
// can be more authoritative than terminal-output heuristics.
type DetectionSource string

const (
	DetectionSourceUnknown   DetectionSource = "unknown"
	DetectionSourceNative    DetectionSource = "native"
	DetectionSourceHeuristic DetectionSource = "heuristic"
	DetectionSourcePattern   DetectionSource = "pattern"
)

// ContainerRef is the provider-neutral durable reference to a runtime container.
// Legacy tmux ownership is retained for history; live refs identify Herdr panes.
type ContainerRef struct {
	Kind      Kind
	Namespace string
	ID        string
	Name      string
	Metadata  string
}

func (r ContainerRef) Target() string {
	if r.ID != "" {
		return r.ID
	}
	return r.Name
}

// LaunchSpec describes a new runtime container to create.
type LaunchSpec struct {
	Kind      Kind
	Namespace string
	Name      string
	CWD       string
	Command   []string
	AgentKind string
	Metadata  string
}

// ReadOptions controls terminal output capture.
type ReadOptions struct {
	Lines int
}

// Detection is a runtime-state observation. Source distinguishes native provider
// state from terminal-output detection.
type Detection struct {
	State   string
	Reason  string
	Excerpt string
	Source  DetectionSource
}

// Interface is the provider-neutral seam for runtime multiplexers.
type Interface interface {
	Kind() Kind
	Ensure(ctx context.Context, namespace string) error
	Launch(ctx context.Context, spec LaunchSpec) (ContainerRef, error)
	Validate(ctx context.Context, ref ContainerRef) (bool, error)
	Focus(ctx context.Context, ref ContainerRef) error
	Read(ctx context.Context, ref ContainerRef, opts ReadOptions) (string, error)
	SendText(ctx context.Context, ref ContainerRef, text string) error
	SendKeys(ctx context.Context, ref ContainerRef, keys ...string) error
	Close(ctx context.Context, ref ContainerRef) error
	Detect(ctx context.Context, ref ContainerRef) (Detection, error)
}

// TerminalPreview is a read-only observation, independent of runtime detection.
type TerminalPreview struct {
	Text       string
	Source     string
	CapturedAt time.Time
	Warning    string
}

// ScreenReader is optional; reads never focus, resize, or send terminal input.
type ScreenReader interface {
	ReadScreen(context.Context, ContainerRef) (string, error)
}
