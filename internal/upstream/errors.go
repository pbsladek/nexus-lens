package upstream

import (
	"fmt"
	"net/http"
	"time"
)

type HTTPError struct {
	Operation  string
	StatusCode int
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	if e.Operation == "" {
		return fmt.Sprintf("upstream returned status %d", e.StatusCode)
	}
	return fmt.Sprintf("%s: upstream returned status %d", e.Operation, e.StatusCode)
}

func NewHTTPError(operation string, resp *http.Response) *HTTPError {
	return &HTTPError{
		Operation:  operation,
		StatusCode: resp.StatusCode,
		RetryAfter: retryAfter(resp.Header.Get("Retry-After")),
	}
}

func retryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := time.ParseDuration(value + "s"); err == nil {
		return seconds
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := time.Until(when)
		if delay > 0 {
			return delay
		}
	}
	return 0
}
