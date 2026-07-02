package indexing

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestRateLimiterIntervalsAndOverrides(t *testing.T) {
	t.Setenv("NEXUS_LENS_RATE_ASSET_LIST_RPS", "10")
	limiter := newRateLimiter()
	if got := limiter.intervalFor("asset_list"); got != 100*time.Millisecond {
		t.Fatalf("asset interval = %s", got)
	}
	if got := limiter.intervalFor("missing"); got != 125*time.Millisecond {
		t.Fatalf("default interval = %s", got)
	}

	rps := 5.0
	control := nexusRateLimitControl{assetListRPS: numericFromPtr(&rps)}
	got, ok := control.rateInterval("asset_list")
	if !ok || got != 200*time.Millisecond {
		t.Fatalf("override interval = %s, %v", got, ok)
	}
	if err := limiter.Wait(context.Background(), "zero-cost-test"); err != nil {
		t.Fatal(err)
	}
	if err := limiter.WaitForNexus(context.Background(), 7, "asset_list", control); err != nil {
		t.Fatal(err)
	}
	if (*rateLimiter)(nil).Wait(context.Background(), "asset_list") != nil {
		t.Fatalf("nil limiter should not wait")
	}
	if got := (*rateLimiter)(nil).intervalFor("asset_list"); got != 0 {
		t.Fatalf("nil limiter interval = %s", got)
	}
}

func TestRateLimiterWaitIntervalHonorsCancellation(t *testing.T) {
	limiter := newRateLimiter()
	limiter.interval["slow"] = time.Hour
	if err := limiter.Wait(context.Background(), "slow"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := limiter.Wait(ctx, "slow"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait err = %v", err)
	}
}

func TestCircuitAndRetryHelpers(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if !circuitOpenUntil(now, 2).After(now) {
		t.Fatalf("expected circuit open time after now")
	}
	t.Setenv("NEXUS_LENS_CIRCUIT_FAILURE_THRESHOLD", "9")
	if circuitThreshold() != 9 {
		t.Fatalf("circuitThreshold did not use env")
	}
	t.Setenv("NEXUS_LENS_CIRCUIT_FAILURE_THRESHOLD", "bad")
	if circuitThreshold() != 3 {
		t.Fatalf("bad circuitThreshold should fall back")
	}
	if !upstreamErrorTriggersCircuit(&TaskError{Retryable: true, Class: "network"}) {
		t.Fatalf("network retryable error should trigger circuit")
	}
	if upstreamErrorTriggersCircuit(&TaskError{Retryable: false, Class: "network"}) || upstreamErrorTriggersCircuit(nil) {
		t.Fatalf("non-retryable/nil errors should not trigger circuit")
	}
	if got := rateLimitError("asset_list", context.Canceled); got.Code != "worker_rate_limit_wait_failed" || !got.Retryable {
		t.Fatalf("rateLimitError = %+v", got)
	}
}

func TestClassifyNetworkError(t *testing.T) {
	taskErr := classifyUpstreamError("fetch", "registry", timeoutError{})
	if taskErr.Code != "registry_network_failed" || taskErr.Class != "network" || !taskErr.Retryable {
		t.Fatalf("taskErr = %+v", taskErr)
	}
	if taskErr.Details["timeout"] != true {
		t.Fatalf("details = %+v", taskErr.Details)
	}

	taskErr = classifyUpstreamError("fetch", "registry", errors.New("boom"))
	if taskErr.Code != "registry_failed" || taskErr.Class != "nexus" || !taskErr.Retryable {
		t.Fatalf("taskErr = %+v", taskErr)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}
