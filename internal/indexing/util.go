package indexing

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

var emptyJSON = []byte("{}")

func text(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func textRequired(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: true}
}

func textValue(value pgtype.Text) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func int8(value int64, valid bool) pgtype.Int8 {
	return pgtype.Int8{Int64: value, Valid: valid}
}

func optionalInt8(value int64) pgtype.Int8 {
	if value <= 0 {
		return pgtype.Int8{}
	}
	return int8(value, true)
}

func int4(value int32, valid bool) pgtype.Int4 {
	return pgtype.Int4{Int32: value, Valid: valid}
}

func optionalInt4(value int32) pgtype.Int4 {
	if value <= 0 {
		return pgtype.Int4{}
	}
	return int4(value, true)
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func pgDate(value time.Time) pgtype.Date {
	utc := value.UTC()
	return pgtype.Date{Time: time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
}

func numericFromPtr(value *float64) pgtype.Numeric {
	if value == nil || *value <= 0 {
		return pgtype.Numeric{}
	}
	var numeric pgtype.Numeric
	if err := numeric.Scan(strconv.FormatFloat(*value, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}
	}
	return numeric
}

func parseNexusTime(value string) pgtype.Timestamptz {
	if value == "" {
		return pgtype.Timestamptz{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return pgtype.Timestamptz{}
	}
	return timestamptz(parsed)
}

func jsonBytes(value any) []byte {
	if value == nil {
		return emptyJSON
	}
	data, err := json.Marshal(value)
	if err != nil {
		return emptyJSON
	}
	return data
}

func newLockToken() (pgtype.UUID, error) {
	var token pgtype.UUID
	if _, err := rand.Read(token.Bytes[:]); err != nil {
		return pgtype.UUID{}, fmt.Errorf("generate task lock token: %w", err)
	}
	token.Valid = true
	return token, nil
}

func workerID() string {
	if value := os.Getenv("NEXUS_LENS_WORKER_ID"); value != "" {
		return value
	}
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		return hostname
	}
	return "worker"
}

func envOrFile(envKey, fileKey string) string {
	if value := os.Getenv(envKey); value != "" {
		return value
	}
	path := os.Getenv(fileKey)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envFloat(key string, fallback float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
