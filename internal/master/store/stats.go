package store

import (
	"context"
	"database/sql"
	"errors"
)

// IncrUpload atomically increments the agent's upstream byte and count counters.
func (s *Store) IncrUpload(ctx context.Context, agentID string, n int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO stats (agent_id, bytes_up, upload_count, last_upload_at)
		 VALUES (?,?,1,?)
		 ON CONFLICT(agent_id) DO UPDATE SET
		   bytes_up=bytes_up+excluded.bytes_up,
		   upload_count=upload_count+1,
		   last_upload_at=excluded.last_upload_at`,
		agentID, n, now())
	return err
}

// GetStats returns the counters for an agent.
func (s *Store) GetStats(ctx context.Context, agentID string) (Stats, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT agent_id, bytes_up, upload_count, last_upload_at FROM stats WHERE agent_id=?`, agentID)
	var st Stats
	err := row.Scan(&st.AgentID, &st.BytesUp, &st.UploadCount, &st.LastUploadAt)
	if errors.Is(err, sql.ErrNoRows) {
		return st, ErrNotFound
	}
	return st, err
}

// AllStats returns a map of agent_id -> Stats for all agents.
func (s *Store) AllStats(ctx context.Context) (map[string]Stats, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT agent_id, bytes_up, upload_count, last_upload_at FROM stats`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]Stats)
	for rows.Next() {
		var st Stats
		if err := rows.Scan(&st.AgentID, &st.BytesUp, &st.UploadCount, &st.LastUploadAt); err != nil {
			return nil, err
		}
		m[st.AgentID] = st
	}
	return m, rows.Err()
}
