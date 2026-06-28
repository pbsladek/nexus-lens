package postgres

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
)

type Config struct {
	URL      string
	Host     string
	Port     int
	Database string
	User     string
	Password string
	SSLMode  string
}

func FromEnv() Config {
	port, _ := strconv.Atoi(envOrDefault("PGPORT", "5432"))
	return Config{
		URL:      os.Getenv("NEXUS_LENS_DATABASE_URL"),
		Host:     os.Getenv("PGHOST"),
		Port:     port,
		Database: os.Getenv("PGDATABASE"),
		User:     os.Getenv("PGUSER"),
		Password: os.Getenv("PGPASSWORD"),
		SSLMode:  envOrDefault("PGSSLMODE", "require"),
	}
}

func (c Config) Enabled() bool {
	return c.URL != "" || c.Host != ""
}

func (c Config) ConnString() (string, error) {
	if c.URL != "" {
		return c.URL, nil
	}
	if c.Host == "" {
		return "", fmt.Errorf("PGHOST or NEXUS_LENS_DATABASE_URL is required")
	}
	if c.Database == "" {
		return "", fmt.Errorf("PGDATABASE is required")
	}
	if c.User == "" {
		return "", fmt.Errorf("PGUSER is required")
	}

	values := url.Values{}
	values.Set("sslmode", envOrDefaultValue(c.SSLMode, "require"))
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, c.Password),
		Host:     fmt.Sprintf("%s:%d", c.Host, c.Port),
		Path:     c.Database,
		RawQuery: values.Encode(),
	}
	return u.String(), nil
}

func (c Config) Redacted() map[string]any {
	return map[string]any{
		"configured": c.Enabled(),
		"url":        c.URL != "",
		"host":       c.Host,
		"port":       c.Port,
		"database":   c.Database,
		"user":       c.User,
		"sslmode":    c.SSLMode,
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envOrDefaultValue(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
