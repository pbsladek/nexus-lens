package migrations

import "embed"

// FS contains the SQL migrations used by goose at application startup.
//
//go:embed *.sql
var FS embed.FS
