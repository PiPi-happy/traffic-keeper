package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // register the "sqlite" driver
)

// Store wraps a SQLite database that persists agents, their policies and
// traffic stats for the traffic-keeper master.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and ensures the schema
// exists. It enables WAL journaling and foreign keys and limits the pool to a
// single connection to avoid SQLITE_BUSY under concurrent writes.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) init() error {
	_, err := s.db.Exec(schemaSQL)
	return err
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS agents (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  token        TEXT NOT NULL UNIQUE,
  secret       TEXT NOT NULL,
  enabled      INTEGER NOT NULL DEFAULT 1,
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL DEFAULT 0,
  last_ip      TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS policies (
  agent_id     TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
  enabled      INTEGER NOT NULL DEFAULT 1,
  interval_sec INTEGER NOT NULL DEFAULT 1800,
  size_mb      INTEGER NOT NULL DEFAULT 50,
  updated_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS stats (
  agent_id       TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
  bytes_up       INTEGER NOT NULL DEFAULT 0,
  upload_count   INTEGER NOT NULL DEFAULT 0,
  last_upload_at INTEGER NOT NULL DEFAULT 0
);
`
