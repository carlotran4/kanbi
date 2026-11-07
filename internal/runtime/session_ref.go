package runtime

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/carlotran4/kanbi/internal/multiplexer"
	"github.com/carlotran4/kanbi/internal/storage"
)

// Accept only official integration reports for this attempt's harness. Never
// confuse the Herdr agent/container name with a native conversation handle.
func nativeSessionRef(detection multiplexer.Detection, harnessName string) string {
	ref := detection.Session
	if ref == nil || ref.Agent != harnessName || ref.Source != "herdr:"+harnessName {
		return ""
	}
	if ref.Kind != "id" && !(harnessName == "pi" && ref.Kind == "path") {
		return ""
	}
	value := strings.TrimSpace(ref.Value)
	if strings.ContainsFunc(value, unicode.IsControl) {
		return ""
	}
	return value
}

func (m *Manager) captureNativeSessionRef(ctx context.Context, ref multiplexer.ContainerRef, harnessName string) (string, bool) {
	if ref.Kind != multiplexer.KindHerdr {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	detection, err := m.herdrAdapter().Detect(ctx, ref)
	if err != nil {
		return "", false
	}
	value := nativeSessionRef(detection, harnessName)
	return value, value != ""
}

func (m *Manager) saveNativeSessionRef(ctx context.Context, ses storage.Session, detection multiplexer.Detection) error {
	if ses.HarnessSessionRef.Valid && strings.TrimSpace(ses.HarnessSessionRef.String) != "" {
		return nil
	}
	if ref := nativeSessionRef(detection, ses.Harness); ref != "" {
		return m.Store.CaptureSessionRef(ctx, ses.ID, ref)
	}
	return nil
}
