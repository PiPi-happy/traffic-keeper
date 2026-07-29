package store

import (
	"context"
	"database/sql"
	"errors"
)

// GetPolicy returns the policy for an agent.
func (s *Store) GetPolicy(ctx context.Context, agentID string) (Policy, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT agent_id, enabled, interval_sec, size_mb, size_min_mb, size_max_mb, updated_at FROM policies WHERE agent_id=?`, agentID)
	var p Policy
	var enabled int64
	err := row.Scan(&p.AgentID, &enabled, &p.IntervalSec, &p.SizeMB, &p.SizeMinMB, &p.SizeMaxMB, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.Enabled = enabled != 0
	return p, err
}

// UpsertPolicy inserts or updates an agent's policy.
func (s *Store) UpsertPolicy(ctx context.Context, p Policy) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO policies (agent_id, enabled, interval_sec, size_mb, size_min_mb, size_max_mb, updated_at)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(agent_id) DO UPDATE SET
		   enabled=excluded.enabled,
		   interval_sec=excluded.interval_sec,
		   size_mb=excluded.size_mb,
		   size_min_mb=excluded.size_min_mb,
		   size_max_mb=excluded.size_max_mb,
		   updated_at=excluded.updated_at`,
		p.AgentID, boolToInt(p.Enabled), p.IntervalSec, p.SizeMB, p.SizeMinMB, p.SizeMaxMB, now())
	return err
}
