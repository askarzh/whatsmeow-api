// Package migrations embeds the SQL migration files for the app store.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed sqlite/*.sql
var sqliteFiles embed.FS

//go:embed postgres/*.sql
var postgresFiles embed.FS

// SQLite returns the embedded SQLite migration files rooted so that file
// names look like "0001_init.up.sql".
func SQLite() fs.FS {
	sub, err := fs.Sub(sqliteFiles, "sqlite")
	if err != nil {
		panic(err)
	}
	return sub
}

// Postgres returns the embedded Postgres migration files rooted so that file
// names look like "0001_init.up.sql".
func Postgres() fs.FS {
	sub, err := fs.Sub(postgresFiles, "postgres")
	if err != nil {
		panic(err)
	}
	return sub
}
