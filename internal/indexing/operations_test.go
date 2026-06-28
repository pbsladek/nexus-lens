package indexing

import (
	"testing"
	"time"

	"github.com/pbsladek/nexus-lens/internal/upstream"
)

func TestClassifyUpstreamHTTPError(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantCode  string
		wantClass string
		retryable bool
	}{
		{name: "auth", status: 401, wantCode: "nexus_auth_failed", wantClass: "auth", retryable: false},
		{name: "not found", status: 404, wantCode: "nexus_not_found", wantClass: "nexus", retryable: false},
		{name: "rate limited", status: 429, wantCode: "nexus_rate_limited", wantClass: "nexus_rate_limit", retryable: true},
		{name: "unavailable", status: 503, wantCode: "nexus_unavailable", wantClass: "nexus_unavailable", retryable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskErr := classifyUpstreamError("test operation", "nexus", &upstream.HTTPError{
				Operation:  "test operation",
				StatusCode: tt.status,
			})
			if taskErr.Code != tt.wantCode || taskErr.Class != tt.wantClass || taskErr.Retryable != tt.retryable {
				t.Fatalf("taskErr = %+v", taskErr)
			}
		})
	}
}

func TestRetryDelayForTaskErrorUsesRetryAfter(t *testing.T) {
	taskErr := &TaskError{RetryAfter: 90 * time.Second}

	if got := retryDelayForTaskError(1, taskErr); got != 90*time.Second {
		t.Fatalf("retry delay = %s, want 90s", got)
	}
}
