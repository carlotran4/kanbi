package ticketbackend

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/carlotran4/kanbi/internal/storage"
)

const (
	defaultProviderHTTPTimeout = 30 * time.Second
	defaultRetryMaxAttempts    = 3
	defaultRetryBaseDelay      = 200 * time.Millisecond
	defaultRetryMaxDelay       = 2 * time.Second
)

// NewProviderHTTPClient returns a client with explicit overall timeout for
// provider calls. Callers still pass request contexts for cancellation.
func NewProviderHTTPClient() *http.Client {
	transport := http.DefaultTransport
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		clone := dt.Clone()
		clone.Proxy = http.ProxyFromEnvironment
		clone.DialContext = (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext
		clone.TLSHandshakeTimeout = 10 * time.Second
		clone.ResponseHeaderTimeout = 20 * time.Second
		clone.ExpectContinueTimeout = 1 * time.Second
		transport = clone
	}
	return &http.Client{
		Timeout:   defaultProviderHTTPTimeout,
		Transport: transport,
	}
}

// methodIsIdempotentGET only allows automatic retries for pure reads in v1.
func methodIsIdempotentGET(method string) bool {
	return strings.EqualFold(method, http.MethodGet)
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	// Exponential backoff with full jitter: [0, min(max, base*2^(attempt-1))]
	exp := float64(defaultRetryBaseDelay) * math.Pow(2, float64(attempt-1))
	if exp > float64(defaultRetryMaxDelay) {
		exp = float64(defaultRetryMaxDelay)
	}
	if exp <= 0 {
		return defaultRetryBaseDelay
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Duration(exp / 2)
	}
	r := float64(binary.BigEndian.Uint64(b[:])) / float64(^uint64(0))
	return time.Duration(r * exp)
}

// shouldRetryClass reports whether a classified error may be retried for method.
func shouldRetryClass(method string, class ErrorClass) bool {
	return methodIsIdempotentGET(method) && class == ClassRetryable
}

func classifyAndWrap(op, method string, status int, attempt int, err error) error {
	class := ClassifyHTTP(status, err)
	cause := err
	if cause == nil {
		cause = fmt.Errorf("%s returned status %d", method, status)
	}
	return &ClassifiedError{
		Class:   class,
		Op:      op,
		Status:  status,
		Attempt: attempt,
		Cause:   fmt.Errorf("%s", storage.RedactSecretText(cause.Error())),
	}
}

// readLimitedBody drains an error response body without retaining secrets wholesale.
func readLimitedBody(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 4096))
	return storage.RedactSecretText(strings.TrimSpace(string(b)))
}

// waitRetry waits for backoff or context cancellation.
func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
