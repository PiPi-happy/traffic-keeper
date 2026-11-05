package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const automationConfigKey = "automation_config"

func (s *Store) GetAutomationConfig(ctx context.Context) (AutomationConfig, error) {
	b, err := s.GetSetting(ctx, automationConfigKey)
	if errors.Is(err, ErrNotFound) {
		return AutomationConfig{TargetMultiplier: 1.3, WindowHours: 24, CheckMinSec: 1800, CheckMaxSec: 5400, MaxTaskMB: 150, Sources: []string{}}, nil
	}
	if err != nil {
		return AutomationConfig{}, err
	}
	var c AutomationConfig
	err = json.Unmarshal([]byte(b), &c)
	return c, err
}

func (s *Store) SetAutomationConfig(ctx context.Context, c AutomationConfig) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, automationConfigKey, string(b))
}

func (s *Store) ListAppSources(ctx context.Context) ([]AppSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,publisher,url,enabled,official,allowed_hosts,max_mb,verified_url FROM app_sources ORDER BY publisher,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppSource{}
	for rows.Next() {
		var a AppSource
		var en, official int
		if err := rows.Scan(&a.ID, &a.Name, &a.Publisher, &a.URL, &en, &official, &a.AllowedHosts, &a.MaxMB, &a.VerifiedURL); err != nil {
			return nil, err
		}
		a.Enabled = en != 0
		a.Official = official != 0
		a.Verified = a.VerifiedURL == a.URL
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) SaveAppSource(ctx context.Context, a AppSource) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO app_sources(id,name,publisher,url,enabled,official,allowed_hosts,max_mb,updated_at,verified_url) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,publisher=excluded.publisher,url=excluded.url,enabled=excluded.enabled,official=excluded.official,allowed_hosts=excluded.allowed_hosts,max_mb=excluded.max_mb,updated_at=excluded.updated_at,verified_url=CASE WHEN app_sources.url=excluded.url AND app_sources.allowed_hosts=excluded.allowed_hosts THEN app_sources.verified_url ELSE '' END`, a.ID, a.Name, a.Publisher, a.URL, boolToInt(a.Enabled), boolToInt(a.Official), a.AllowedHosts, a.MaxMB, time.Now().Unix(), a.VerifiedURL)
	return err
}

func (s *Store) MarkSourceVerified(ctx context.Context, id, url string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE app_sources SET verified_url=? WHERE id=? AND url=?`, url, id, url)
	return err
}

func (s *Store) SeedAppSources(ctx context.Context, sources []AppSource) error {
	for _, a := range sources {
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO app_sources(id,name,publisher,url,enabled,official,allowed_hosts,max_mb,updated_at,verified_url) VALUES(?,?,?,?,?,?,?,?,?,?)`, a.ID, a.Name, a.Publisher, a.URL, boolToInt(a.Enabled), boolToInt(a.Official), a.AllowedHosts, a.MaxMB, time.Now().Unix(), a.VerifiedURL); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteAppSource(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_sources WHERE id=?`, id)
	return err
}

func (s *Store) InsertAutomationEvent(ctx context.Context, e AutomationEvent) error {
	if e.StartedAt == 0 {
		e.StartedAt = time.Now().Unix()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO automation_events(agent_id,source_id,app_name,started_at,bytes,status,error,duration_ms) VALUES(?,?,?,?,?,?,?,?)`, e.AgentID, e.SourceID, e.AppName, e.StartedAt, e.Bytes, e.Status, e.Error, e.DurationMs)
	return err
}

func (s *Store) DeleteAutomationEventsBefore(ctx context.Context, ts int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM automation_events WHERE started_at < ?`, ts)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return n, err
}

func (s *Store) ListAutomationEvents(ctx context.Context, since int64, limit int) ([]AutomationEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,agent_id,source_id,app_name,started_at,bytes,status,error,duration_ms FROM automation_events WHERE started_at>=? ORDER BY started_at DESC LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutomationEvent{}
	for rows.Next() {
		var e AutomationEvent
		if err := rows.Scan(&e.ID, &e.AgentID, &e.SourceID, &e.AppName, &e.StartedAt, &e.Bytes, &e.Status, &e.Error, &e.DurationMs); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
