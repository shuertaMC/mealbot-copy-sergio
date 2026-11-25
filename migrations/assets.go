// Package migrations exposes the ordered SQL migration assets used by the
// PostgreSQL store. Schema migrations are deliberately separate from the
// public `mealbot migrate` pair-history reconstruction command.
package migrations

import "embed"

// Files contains every numbered migration in this directory.
//
//go:embed *.sql
var Files embed.FS
