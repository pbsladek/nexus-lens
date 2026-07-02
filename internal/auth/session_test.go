package auth

import (
	"testing"
	"time"
)

func TestSessionTokenGenerationAndHashing(t *testing.T) {
	token, hash, err := newSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || hash == "" || token == hash {
		t.Fatalf("token=%q hash=%q", token, hash)
	}
	if hashSessionToken(token) != hash {
		t.Fatalf("hashSessionToken did not reproduce hash")
	}
	if hashSessionToken(token) == hashSessionToken(token+"x") {
		t.Fatalf("different tokens produced same hash")
	}
}

func TestAuthTimestamptz(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	value := timestamptz(now)
	if !value.Valid || !value.Time.Equal(now) {
		t.Fatalf("timestamptz = %+v", value)
	}
}
