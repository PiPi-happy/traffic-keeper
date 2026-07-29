package store

import (
	"database/sql"
	"fmt"
)

// migrate adds columns introduced after the initial schema to pre-existing
// databases (the live v0.3.0 DB has the original columns only; CREATE TABLE IF
// NOT EXISTS won't add new ones). ALTER TABLE ADD COLUMN is idempotent-ish: we
// check PRAGMA table_info first.
func (s *Store) migrate() error {
	add := []struct{ table, col, decl string }{
		{"agents", "version", "TEXT NOT NULL DEFAULT ''"},
		{"agents", "pending_upgrade", "TEXT NOT NULL DEFAULT ''"},
		{"policies", "size_min_mb", "INTEGER NOT NULL DEFAULT 0"},
		{"policies", "size_max_mb", "INTEGER NOT NULL DEFAULT 0"},
		{"upload_events", "duration_ms", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, c := range add {
		exists, err := s.columnExists(c.table, c.col)
		if err != nil {
			return fmt.Errorf("migrate check %s.%s: %w", c.table, c.col, err)
		}
		if exists {
			continue
		}
		if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.col, c.decl)); err != nil {
			return fmt.Errorf("migrate add %s.%s: %w", c.table, c.col, err)
		}
	}
	return nil
}

func (s *Store) columnExists(table, col string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}
