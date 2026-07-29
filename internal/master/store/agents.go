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

const agentCols = "id, name, token, secret, enabled, created_at, last_seen_at, last_ip, version, pending_upgrade"

func scanAgent(row scanner) (Agent, error) {
	var a Agent
	var enabled int64
	err := row.Scan(&a.ID, &a.Name, &a.Token, &a.Secret, &enabled, &a.CreatedAt, &a.LastSeenAt, &a.LastIP, &a.Version, &a.PendingUpgrade)
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
		`INSERT INTO agents (id, name, token, secret, enabled, created_at, last_seen_at, last_ip, version, pending_upgrade)
		 VALUES (?,?,?,?,1,?,0,'','','')`,
		a.ID, a.Name, a.Token, a.Secret, a.CreatedAt); err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO policies (agent_id, enabled, interval_sec, size_mb, size_min_mb, size_max_mb, updated_at) VALUES (?,1,1800,50,0,0,?)`,
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
	row := s.db.QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents WHERE id=?`, id)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// GetAgentByToken fetches an agent by its install token.
func (s *Store) GetAgentByToken(ctx context.Context, token string) (Agent, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents WHERE token=?`, token)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// ListAgents returns all agents ordered by creation time.
func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+agentCols+` FROM agents ORDER BY created_at`)
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

// TouchAgent updates the agent's last-seen timestamp, source IP, and version.
func (s *Store) TouchAgent(ctx context.Context, id, ip, version string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET last_seen_at=?, last_ip=?, version=? WHERE id=?`, now(), ip, version, id)
	return err
}

// SetAgentEnabled enables or disables an agent.
func (s *Store) SetAgentEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET enabled=? WHERE id=?`, boolToInt(enabled), id)
	return err
}

// SetPendingUpgrade marks an agent to self-upgrade to the given version.
func (s *Store) SetPendingUpgrade(ctx context.Context, id, version string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET pending_upgrade=? WHERE id=?`, version, id)
	return err
}

// ClearPendingUpgrade clears any pending self-upgrade.
func (s *Store) ClearPendingUpgrade(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET pending_upgrade='' WHERE id=?`, id)
	return err
}

// DeleteAgent removes an agent; foreign keys cascade-delete its policy and stats.
func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agents WHERE id=?`, id)
	return err
}
