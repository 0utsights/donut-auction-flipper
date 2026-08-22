package migrations

import "embed"

// Files is the canonical ordered PostgreSQL migration set used both by the
// backend and the database container's first-start initialization.
//
//go:embed *.sql
var Files embed.FS
