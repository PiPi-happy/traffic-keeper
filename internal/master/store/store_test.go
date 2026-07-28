package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestStoreCRUD(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.CreateAgent(ctx, Agent{ID: "a1", Name: "node1", Token: "tok1", Secret: "sec1"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetAgent(ctx, "a1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "node1" || !got.Enabled {
		t.Fatalf("unexpected agent: %+v", got)
	}

	if bt, _ := s.GetAgentByToken(ctx, "tok1"); bt.ID != "a1" {
		t.Fatalf("get by token: %+v", bt)
	}

	// default policy seeded by CreateAgent
	p, err := s.GetPolicy(ctx, "a1")
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if !p.Enabled || p.IntervalSec != 1800 || p.SizeMB != 50 {
		t.Fatalf("default policy: %+v", p)
	}

	// upsert policy
	if err := s.UpsertPolicy(ctx, Policy{AgentID: "a1", Enabled: false, IntervalSec: 600, SizeMB: 20}); err != nil {
		t.Fatalf("upsert policy: %v", err)
	}
	if p2, _ := s.GetPolicy(ctx, "a1"); p2.Enabled || p2.IntervalSec != 600 || p2.SizeMB != 20 {
		t.Fatalf("updated policy: %+v", p2)
	}

	// increment stats twice
	if err := s.IncrUpload(ctx, "a1", 1024); err != nil {
		t.Fatalf("incr1: %v", err)
	}
	if err := s.IncrUpload(ctx, "a1", 2048); err != nil {
		t.Fatalf("incr2: %v", err)
	}
	st, err := s.GetStats(ctx, "a1")
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if st.BytesUp != 3072 || st.UploadCount != 2 {
		t.Fatalf("stats: %+v", st)
	}

	// touch + list
	if err := s.TouchAgent(ctx, "a1", "1.2.3.4"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if list, err := s.ListAgents(ctx); err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}

	// delete cascades to policy + stats
	if err := s.DeleteAgent(ctx, "a1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetAgent(ctx, "a1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	if _, err := s.GetPolicy(ctx, "a1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected policy cascade delete, got %v", err)
	}
	if _, err := s.GetStats(ctx, "a1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected stats cascade delete, got %v", err)
	}
}
