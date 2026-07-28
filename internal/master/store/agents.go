package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound is returned when no row matches the query.
var ErrNotFound = errors.New("store: not found")

type scanner interface {
	Scan(dest ...any) error
}

func scanAgent(row scanner) (Agent, error) {
	var a Agent
	var enabled int64
	err := row.Scan(&a.ID, &a.Name, &a.Token, &a.Secret, &enabled, &a.CreatedAt, &a.LastSeenAt, &a.LastIP)
	a.Enabled = enabled != 0
	return a, err
}

// CreateAgent inserts a new agent and seeds default policy and stats rows in
// a single transaction.
func (s *Store) CreateAgent(ctx context.Context, a Agent) error {
	if a.CreatedAt == 0 {
		a.CreatedAt = now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agents (id, name, token, secret, enabled, created_at, last_seen_at, last_ip)
		 VALUES (?,?,?,?,1,?,0,'')`,
		a.ID, a.Name, a.Token, a.Secret, a.CreatedAt); err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO policies (agent_id, enabled, interval_sec, size_mb, updated_at) VALUES (?,1,1800,50,?)`,
		a.ID, now()); err != nil {
		return fmt.Errorf("seed policy: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO stats (agent_id) VALUES (?)`, a.ID); err != nil {
		return fmt.Errorf("seed stats: %w", err)
	}
	return tx.Commit()
}

// GetAgent fetches an agent by id.
func (s *Store) GetAgent(ctx context.Context, id string) (Agent, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, token, secret, enabled, created_at, last_seen_at, last_ip FROM agents WHERE id=?`, id)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// GetAgentByToken fetches an agent by its install token.
func (s *Store) GetAgentByToken(ctx context.Context, token string) (Agent, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, token, secret, enabled, created_at, last_seen_at, last_ip FROM agents WHERE token=?`, token)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// ListAgents returns all agents ordered by creation time.
func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, token, secret, enabled, created_at, last_seen_at, last_ip FROM agents ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// TouchAgent updates the agent's last-seen timestamp and source IP.
func (s *Store) TouchAgent(ctx context.Context, id, ip string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET last_seen_at=?, last_ip=? WHERE id=?`, now(), ip, id)
	return err
}

// SetAgentEnabled enables or disables an agent.
func (s *Store) SetAgentEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET enabled=? WHERE id=?`, boolToInt(enabled), id)
	return err
}

// DeleteAgent removes an agent; foreign keys cascade-delete its policy and stats.
func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agents WHERE id=?`, id)
	return err
}
