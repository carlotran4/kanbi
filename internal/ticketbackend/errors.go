package ticketbackend

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/carlotran4/kanbi/internal/storage"
)

// ErrorClass classifies provider operation failures for retry policy.
type ErrorClass int

const (
	ClassUnknown ErrorClass = iota
	ClassRetryable
	ClassAuth
	ClassValidation
	ClassConflict
	ClassCanceled
)

func (c ErrorClass) String() string {
	switch c {
	case ClassRetryable:
		return "retryable"
	case ClassAuth:
		return "auth"
	case ClassValidation:
		return "validation"
	case ClassConflict:
		return "conflict"
	case ClassCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}

// ClassifiedError is a provider operation failure with redacted display text.
type ClassifiedError struct {
	Class    ErrorClass
	Op       string
	BoardID  int64
	TicketID int64
	Attempt  int
	Status   int
	Cause    error
}

func (e *ClassifiedError) Error() string {
	if e == nil {
		return ""
	}
	op := e.Op
	if op == "" {
		op = "provider request"
	}
	msg := fmt.Sprintf("%s failed (%s)", op, e.Class.String())
	if e.Status > 0 {
		msg = fmt.Sprintf("%s status=%d", msg, e.Status)
	}
	if e.Attempt > 0 {
		msg = fmt.Sprintf("%s attempt=%d", msg, e.Attempt)
	}
	if e.Cause != nil {
		msg = fmt.Sprintf("%s: %s", msg, storage.RedactSecretText(e.Cause.Error()))
	}
	return msg
}

func (e *ClassifiedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// ClassifyHTTP maps HTTP transport/status outcomes to a retry class.
func ClassifyHTTP(status int, err error) ErrorClass {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ClassCanceled
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			if netErr.Timeout() {
				return ClassRetryable
			}
		}
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			if urlErr.Timeout() {
				return ClassRetryable
			}
			if urlErr.Err != nil && (errors.Is(urlErr.Err, context.Canceled) || errors.Is(urlErr.Err, context.DeadlineExceeded)) {
				return ClassCanceled
			}
			// transient transport failure
			return ClassRetryable
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "timeout") ||
			strings.Contains(msg, "temporary") ||
			strings.Contains(msg, "connection reset") ||
			strings.Contains(msg, "connection refused") ||
			strings.Contains(msg, "tls handshake") {
			return ClassRetryable
		}
		return ClassUnknown
	}
	switch status {
	case 401, 403:
		return ClassAuth
	case 400, 422:
		return ClassValidation
	case 409:
		return ClassConflict
	case 408, 429:
		return ClassRetryable
	default:
		if status >= 500 && status <= 504 {
			return ClassRetryable
		}
		if status >= 200 && status < 300 {
			return ClassUnknown
		}
		if status >= 400 && status < 500 {
			return ClassValidation
		}
		return ClassUnknown
	}
}

// IsRetryable reports whether an error was classified as retryable.
func IsRetryable(err error) bool {
	var ce *ClassifiedError
	return errors.As(err, &ce) && ce.Class == ClassRetryable
}
