package store

import "embed"

// postgresMigrationFiles is the single embedded authority for PostgreSQL
// application schema. The runner is introduced in the following change.
//
//go:embed migrations/*.sql
var postgresMigrationFiles embed.FS

var postgresSchema = mustReadPostgresMigration("migrations/0002_reconcile_current.sql")

func mustReadPostgresMigration(name string) string {
	raw, err := postgresMigrationFiles.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
