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

// HourlyBucket is one hour-bucketed total of successful upload bytes.
type HourlyBucket struct {
	Hour  int64 // unix timestamp floored to the hour
	Bytes int64
}

// HourlyBytes returns total ok upload bytes per hour (floored to the hour) for
// events with ts>=since, ordered by hour ascending. Used for the 24h trend.
func (s *Store) HourlyBytes(ctx context.Context, since int64) ([]HourlyBucket, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT (ts/3600)*3600 AS hour, COALESCE(SUM(bytes),0) FROM upload_events
		 WHERE ts>=? AND status='ok' GROUP BY hour ORDER BY hour`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HourlyBucket
	for rows.Next() {
		var b HourlyBucket
		if err := rows.Scan(&b.Hour, &b.Bytes); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CountByStatus returns the ok/fail event counts for events with ts>=since.
func (s *Store) CountByStatus(ctx context.Context, since int64) (ok, fail int64, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM upload_events WHERE ts>=? GROUP BY status`, since)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			return 0, 0, err
		}
		switch status {
		case "ok":
			ok = n
		case "fail":
			fail = n
		}
	}
	return ok, fail, rows.Err()
}

// BytesSince returns total ok upload bytes for events with ts>=since. Used to
// derive the recent average upload rate.
func (s *Store) BytesSince(ctx context.Context, since int64) (int64, error) {
	var sum int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(bytes),0) FROM upload_events WHERE ts>=? AND status='ok'`, since).Scan(&sum)
	return sum, err
}
