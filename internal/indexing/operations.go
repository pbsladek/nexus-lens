package indexing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pbsladek/nexus-lens/internal/upstream"
)

type rateLimiter struct {
	mu       sync.Mutex
	last     map[string]time.Time
	interval map[string]time.Duration
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		last: map[string]time.Time{},
		interval: map[string]time.Duration{
			"asset_list":        envRateInterval("NEXUS_LENS_RATE_ASSET_LIST_RPS", 4),
			"registry_manifest": envRateInterval("NEXUS_LENS_RATE_REGISTRY_MANIFEST_RPS", 2),
			"default":           envRateInterval("NEXUS_LENS_RATE_DEFAULT_RPS", 8),
		},
	}
}

func (l *rateLimiter) Wait(ctx context.Context, costClass string) error {
	interval := l.intervalFor(costClass)
	return l.waitInterval(ctx, costClass, interval)
}

func (l *rateLimiter) WaitForNexus(ctx context.Context, nexusInstanceID int64, costClass string, control nexusRateLimitControl) error {
	interval := l.intervalFor(costClass)
	if override, ok := control.rateInterval(costClass); ok {
		interval = override
	}
	key := fmt.Sprintf("%d/%s", nexusInstanceID, costClass)
	return l.waitInterval(ctx, key, interval)
}

func (l *rateLimiter) intervalFor(costClass string) time.Duration {
	if l == nil {
		return 0
	}
	interval := l.interval[costClass]
	if interval <= 0 {
		interval = l.interval["default"]
	}
	return interval
}

func (l *rateLimiter) waitInterval(ctx context.Context, key string, interval time.Duration) error {
	if l == nil {
		return nil
	}
	if interval <= 0 {
		return nil
	}

	l.mu.Lock()
	now := time.Now()
	next := l.last[key].Add(interval)
	if next.Before(now) {
		l.last[key] = now
		l.mu.Unlock()
		return nil
	}
	wait := time.Until(next)
	l.last[key] = next
	l.mu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c nexusRateLimitControl) RateLimitNumeric(costClass string) pgtype.Numeric {
	switch costClass {
	case "asset_list":
		return c.assetListRPS
	case "registry_manifest":
		return c.registryManifestRPS
	default:
		return c.defaultRPS
	}
}

type nexusRateLimitControl struct {
	assetListRPS        pgtype.Numeric
	registryManifestRPS pgtype.Numeric
	defaultRPS          pgtype.Numeric
}

func (c nexusRateLimitControl) rateInterval(costClass string) (time.Duration, bool) {
	numeric := c.RateLimitNumeric(costClass)
	if !numeric.Valid {
		numeric = c.defaultRPS
	}
	if !numeric.Valid {
		return 0, false
	}
	value, err := numeric.Float64Value()
	if err != nil || !value.Valid || value.Float64 <= 0 {
		return 0, false
	}
	return time.Duration(float64(time.Second) / value.Float64), true
}

func envRateInterval(key string, fallbackRPS float64) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	rps := fallbackRPS
	if value != "" {
		parsed, err := strconv.ParseFloat(value, 64)
		if err == nil {
			rps = parsed
		}
	}
	if rps <= 0 {
		return 0
	}
	return time.Duration(float64(time.Second) / rps)
}

func classifyUpstreamError(operation, codePrefix string, err error) *TaskError {
	var httpErr *upstream.HTTPError
	if errors.As(err, &httpErr) {
		status := httpErr.StatusCode
		details := map[string]any{
			"operation":   operation,
			"status_code": status,
		}
		if httpErr.RetryAfter > 0 {
			details["retry_after_ms"] = httpErr.RetryAfter.Milliseconds()
		}
		switch {
		case status == 401 || status == 403:
			return &TaskError{Code: codePrefix + "_auth_failed", Class: "auth", Message: err.Error(), Retryable: false, Details: details}
		case status == 404:
			return &TaskError{Code: codePrefix + "_not_found", Class: "nexus", Message: err.Error(), Retryable: false, Details: details}
		case status == 408 || status == 429:
			return &TaskError{Code: codePrefix + "_rate_limited", Class: "nexus_rate_limit", Message: err.Error(), Retryable: true, RetryAfter: httpErr.RetryAfter, Details: details}
		case status >= 500:
			return &TaskError{Code: codePrefix + "_unavailable", Class: "nexus_unavailable", Message: err.Error(), Retryable: true, RetryAfter: httpErr.RetryAfter, Details: details}
		default:
			return &TaskError{Code: codePrefix + "_http_error", Class: "nexus", Message: err.Error(), Retryable: false, Details: details}
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return &TaskError{
			Code:      codePrefix + "_network_failed",
			Class:     "network",
			Message:   err.Error(),
			Retryable: true,
			Details: map[string]any{
				"operation": operation,
				"timeout":   netErr.Timeout(),
			},
		}
	}

	return &TaskError{
		Code:      codePrefix + "_failed",
		Class:     "nexus",
		Message:   err.Error(),
		Retryable: true,
		Details:   map[string]any{"operation": operation},
	}
}

func retryDelayForTaskError(attempt int32, taskErr *TaskError) time.Duration {
	if taskErr != nil && taskErr.RetryAfter > 0 {
		return clampDuration(taskErr.RetryAfter, time.Second, 5*time.Minute)
	}
	return retryDelay(attempt)
}

func circuitOpenUntil(now time.Time, failureCount int32) time.Time {
	if failureCount < 1 {
		failureCount = 1
	}
	seconds := math.Min(float64(300), math.Pow(2, float64(failureCount))*5)
	return now.Add(time.Duration(seconds) * time.Second)
}

func clampDuration(value, min, max time.Duration) time.Duration {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func circuitThreshold() int32 {
	value := strings.TrimSpace(os.Getenv("NEXUS_LENS_CIRCUIT_FAILURE_THRESHOLD"))
	if value == "" {
		return 3
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 3
	}
	return int32(parsed)
}

func upstreamErrorTriggersCircuit(taskErr *TaskError) bool {
	if taskErr == nil || !taskErr.Retryable {
		return false
	}
	return taskErr.Class == "nexus_rate_limit" || taskErr.Class == "nexus_unavailable" || taskErr.Class == "network"
}

func rateLimitError(costClass string, err error) *TaskError {
	return &TaskError{
		Code:      "worker_rate_limit_wait_failed",
		Class:     "worker",
		Message:   fmt.Sprintf("wait for %s rate limit: %v", costClass, err),
		Retryable: true,
		Details:   map[string]any{"cost_class": costClass},
	}
}
