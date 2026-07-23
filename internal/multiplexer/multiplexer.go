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
	DetectionSourceTmux      DetectionSource = "tmux"
	DetectionSourceHeuristic DetectionSource = "heuristic"
	DetectionSourcePattern   DetectionSource = "pattern"
)

// Confidence ranks how strongly a Detection should be trusted.
type Confidence string

const (
	ConfidenceUnknown Confidence = "unknown"
	ConfidenceLow     Confidence = "low"
	ConfidenceMedium  Confidence = "medium"
	ConfidenceHigh    Confidence = "high"
)

// ContainerRef is the provider-neutral durable reference to a runtime container.
// For tmux, Namespace is the session name and ID/Name are window id/name.
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
	Since time.Time
	Lines int
}

// Detection is a provider-neutral runtime-state observation. The Source and
// Confidence fields make room for future authoritative provider state while tmux
// pane output remains a fallback.
type Detection struct {
	State      string
	Reason     string
	Excerpt    string
	Source     DetectionSource
	Confidence Confidence
	ObservedAt time.Time
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
