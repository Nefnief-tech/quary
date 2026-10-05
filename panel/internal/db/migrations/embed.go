// Package migrations embeds the goose SQL migration files. The embed lives
// in this directory (not the parent) because goose's provider globs the fsys
// root for *.sql files.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
