// Package diagnostics provides opt-in, bounded file logging for Kanbi.
// Logging is disabled by default and never runs as a background daemon.
package diagnostics

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Level is a severity for diagnosed log records.
type Level int

const (
	LevelOff Level = iota
	LevelError
	LevelWarn
	LevelInfo
	LevelDebug
)

// ParseLevel accepts off|error|warn|info|debug (case-insensitive).
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "off", "none", "disabled":
		return LevelOff, nil
	case "error", "err", "fatal":
		return LevelError, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "info":
		return LevelInfo, nil
	case "debug", "trace":
		return LevelDebug, nil
	default:
		return LevelOff, fmt.Errorf("unknown diagnostics log level %q", s)
	}
}

func (l Level) String() string {
	switch l {
	case LevelError:
		return "error"
	case LevelWarn:
		return "warn"
	case LevelInfo:
		return "info"
	case LevelDebug:
		return "debug"
	default:
		return "off"
	}
}

// Options configures the file logger.
type Options struct {
	// Dir is the directory where kanbi-diagnostics.log is written (usually StateDir).
	Dir string
	// Level is the minimum severity to emit. LevelOff disables writing.
	Level Level
	// MaxBytes is the soft size before rotating the active log (default 1 MiB).
	MaxBytes int64
	// MaxFiles is the number of rotated files to retain besides the active log (default 3).
	MaxFiles int
}

const (
	defaultMaxBytes = 1 << 20 // 1 MiB
	defaultMaxFiles = 3
	logFileName     = "kanbi-diagnostics.log"
)

// Record is one structured diagnostic log line.
type Record struct {
	Time        time.Time         `json:"time"`
	Severity    string            `json:"severity"`
	Subsystem   string            `json:"subsystem"`
	Operation   string            `json:"operation,omitempty"`
	Correlation string            `json:"correlation,omitempty"`
	Message     string            `json:"message"`
	Fields      map[string]string `json:"fields,omitempty"`
}

// Logger writes bounded, private diagnostic logs when enabled.
type Logger struct {
	mu      sync.Mutex
	opts    Options
	enabled bool
}

// New returns a logger. LevelOff or empty Dir yields a no-op logger.
func New(opts Options) *Logger {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultMaxBytes
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = defaultMaxFiles
	}
	l := &Logger{opts: opts}
	if opts.Level > LevelOff && strings.TrimSpace(opts.Dir) != "" {
		l.enabled = true
	}
	return l
}

// Enabled reports whether file logging is active.
func (l *Logger) Enabled() bool {
	return l != nil && l.enabled
}

// LogPath returns the active log file path, if configured.
func (l *Logger) LogPath() string {
	if l == nil || strings.TrimSpace(l.opts.Dir) == "" {
		return ""
	}
	return filepath.Join(l.opts.Dir, logFileName)
}

// Error writes an error-level record when logging is enabled and level allows.
func (l *Logger) Error(subsystem, operation, correlation, message string, fields map[string]string) {
	l.log(LevelError, subsystem, operation, correlation, message, fields)
}

// Warn writes a warn-level record.
func (l *Logger) Warn(subsystem, operation, correlation, message string, fields map[string]string) {
	l.log(LevelWarn, subsystem, operation, correlation, message, fields)
}

// Info writes an info-level record.
func (l *Logger) Info(subsystem, operation, correlation, message string, fields map[string]string) {
	l.log(LevelInfo, subsystem, operation, correlation, message, fields)
}

// Debug writes a debug-level record.
func (l *Logger) Debug(subsystem, operation, correlation, message string, fields map[string]string) {
	l.log(LevelDebug, subsystem, operation, correlation, message, fields)
}

func (l *Logger) log(level Level, subsystem, operation, correlation, message string, fields map[string]string) {
	if l == nil || !l.enabled || level > l.opts.Level || level == LevelOff {
		return
	}
	rec := Record{
		Time:        time.Now().UTC(),
		Severity:    level.String(),
		Subsystem:   strings.TrimSpace(subsystem),
		Operation:   strings.TrimSpace(operation),
		Correlation: strings.TrimSpace(correlation),
		Message:     strings.TrimSpace(message),
	}
	if rec.Subsystem == "" {
		rec.Subsystem = "kanbi"
	}
	if len(fields) > 0 {
		rec.Fields = fields
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.writeLineLocked(append(line, '\n'))
}

func (l *Logger) writeLineLocked(line []byte) error {
	if err := os.MkdirAll(l.opts.Dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(l.opts.Dir, logFileName)
	if err := rotateIfNeeded(path, l.opts.MaxBytes, l.opts.MaxFiles); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return err
	}
	_ = f.Chmod(0o600)
	return nil
}

func rotateIfNeeded(path string, maxBytes int64, maxFiles int) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() < maxBytes {
		return nil
	}
	// log -> log.1 -> log.2 ... drop the oldest.
	_ = os.Remove(fmt.Sprintf("%s.%d", path, maxFiles))
	for i := maxFiles - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", path, i)
		dst := fmt.Sprintf("%s.%d", path, i+1)
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, dst)
		}
	}
	return os.Rename(path, path+".1")
}

// ReadRecent returns the last maxBytes of the active log (best-effort, may be empty).
func ReadRecent(dir string, maxBytes int64) ([]byte, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	path := filepath.Join(dir, logFileName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	start := int64(0)
	if size > maxBytes {
		start = size - maxBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, size-start)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		if n == 0 {
			return nil, err
		}
	}
	return buf[:n], nil
}
