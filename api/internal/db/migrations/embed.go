// Package migrations embeds a synced copy of db/migrations (repo root, the
// source of truth) so the loomtale CLI can embed goose SQL without a
// go:embed ".." path, which the toolchain forbids. `make gen` copies the
// root SQL files here; `make gen-check` fails CI on drift.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
