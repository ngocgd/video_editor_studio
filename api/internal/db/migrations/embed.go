// Package migrations embeds a synced copy of db/migrations (repo root, the
// source of truth) so the loomtale CLI can embed goose SQL. The embed
// directive below cannot reference a parent directory, which is why this
// package holds a copy instead of the original files. `make gen` copies the
// root SQL files here; `make gen-check` fails CI on drift.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
