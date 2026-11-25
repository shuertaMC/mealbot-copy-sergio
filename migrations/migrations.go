// Package migrations exposes the ordered SQL migrations embedded in the
// Mealbot binary. Execution and ledger management live in the PostgreSQL
// adapter so this package has no database dependency.
package migrations

import "embed"

// Files contains the numbered forward-only SQL migration files.
//
//go:embed *.sql
var Files embed.FS
