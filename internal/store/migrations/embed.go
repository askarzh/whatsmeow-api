// Package migrations embeds the SQL migration files for the app store.
// Plan 10 will add a `postgres/` sibling.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed sqlite/*.sql
var sqliteFiles embed.FS

// SQLite returns the embedded migration files rooted so that file names look
// like "0001_init.up.sql" (rather than "sqlite/0001_init.up.sql").
// golang-migrate's iofs source expects this layout.
func SQLite() fs.FS {
	sub, err := fs.Sub(sqliteFiles, "sqlite")
	if err != nil {
		// Compile-time impossibility: the //go:embed directive is fixed.
		panic(err)
	}
	return sub
}
