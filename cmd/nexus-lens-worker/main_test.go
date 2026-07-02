package main

import "testing"

func TestWorkerEnvHelpers(t *testing.T) {
	t.Setenv("WORKER_VALUE", "value")
	t.Setenv("WORKER_INTERVAL", "300")
	t.Setenv("WORKER_INTERVAL_BAD", "bad")
	t.Setenv("WORKER_INTERVAL_ZERO", "0")

	if got := envOrDefault("WORKER_VALUE", "fallback"); got != "value" {
		t.Fatalf("envOrDefault = %q", got)
	}
	if got := envOrDefault("WORKER_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("envOrDefault fallback = %q", got)
	}
	if got := envInt32OrDefault("WORKER_INTERVAL", 60); got != 300 {
		t.Fatalf("envInt32OrDefault = %d", got)
	}
	if got := envInt32OrDefault("WORKER_INTERVAL_BAD", 60); got != 60 {
		t.Fatalf("bad envInt32OrDefault = %d", got)
	}
	if got := envInt32OrDefault("WORKER_INTERVAL_ZERO", 60); got != 60 {
		t.Fatalf("zero envInt32OrDefault = %d", got)
	}
}
