package upstream

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewHTTPErrorCapturesStatusAndRetryAfter(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Status:     "429 Too Many Requests",
		Header:     http.Header{"Retry-After": []string{"7"}},
	}

	err := NewHTTPError("list assets", resp)
	if err.Operation != "list assets" || err.StatusCode != http.StatusTooManyRequests || err.RetryAfter != 7*time.Second {
		t.Fatalf("http error = %+v", err)
	}
	if !strings.Contains(err.Error(), "list assets") || !strings.Contains(err.Error(), "429") {
		t.Fatalf("error string = %q", err.Error())
	}
}

func TestRetryAfterParsesHTTPDateAndInvalidValues(t *testing.T) {
	future := time.Now().UTC().Add(2 * time.Minute).Format(http.TimeFormat)
	if retryAfter(future) <= 0 {
		t.Fatalf("expected positive retry-after for future date")
	}
	if retryAfter("") != 0 || retryAfter("not-a-date") != 0 {
		t.Fatalf("expected invalid retry-after values to be ignored")
	}
}
