package store

import (
	"context"
)

// Event is one upload attempt recorded for an agent.
type Event struct {
	ID         int64
	AgentID    string
	Ts         int64
	Bytes      int64
	Status     string // "ok" or "fail"
	Error      string
	DurationMs int64 // master-side receive duration
}

// InsertEvent records an upload event.
func (s *Store) InsertEvent(ctx context.Context, e Event) error {
	if e.Ts == 0 {
		e.Ts = now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO upload_events (agent_id, ts, bytes, status, error, duration_ms) VALUES (?,?,?,?,?,?)`,
		e.AgentID, e.Ts, e.Bytes, e.Status, e.Error, e.DurationMs)
	return err
}

// ListEvents returns up to limit events for agentID with ts >= since, newest first.
func (s *Store) ListEvents(ctx context.Context, agentID string, since, limit int64) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_id, ts, bytes, status, error, duration_ms FROM upload_events
		 WHERE agent_id=? AND ts>=? ORDER BY ts DESC LIMIT ?`, agentID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Ts, &e.Bytes, &e.Status, &e.Error, &e.DurationMs); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// DeleteEventsBefore removes events older than the given unix timestamp.
func (s *Store) DeleteEventsBefore(ctx context.Context, before int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM upload_events WHERE ts<?`, before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
