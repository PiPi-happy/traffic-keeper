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

	// touch + list (TouchAgent now also records version)
	if err := s.TouchAgent(ctx, "a1", "1.2.3.4", "v9.9.9", "CN", "amd64"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if got, _ := s.GetAgent(ctx, "a1"); got.Version != "v9.9.9" {
		t.Fatalf("version not stored: %q", got.Version)
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

func TestSettings(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if _, err := s.GetSetting(ctx, "k1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unset should be ErrNotFound, got %v", err)
	}
	if err := s.SetSetting(ctx, "k1", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetSetting(ctx, "k1"); v != "v1" {
		t.Fatalf("got %q", v)
	}
	if err := s.SetSetting(ctx, "k1", "v2"); err != nil { // upsert
		t.Fatal(err)
	}
	if v, _ := s.GetSetting(ctx, "k1"); v != "v2" {
		t.Fatalf("upsert got %q", v)
	}
}

func TestEvents(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.CreateAgent(ctx, Agent{ID: "a1", Name: "n", Token: "t", Secret: "x"}); err != nil {
		t.Fatal(err)
	}

	if err := s.InsertEvent(ctx, Event{AgentID: "a1", Bytes: 100, Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertEvent(ctx, Event{AgentID: "a1", Bytes: 0, Status: "fail", Error: "read err"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListEvents(ctx, "a1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Status != "fail" { // newest first
		t.Fatalf("order: %+v", got)
	}

	// delete-old + cascade-on-agent-delete
	if n, _ := s.DeleteEventsBefore(ctx, got[0].Ts+1); n != 2 {
		t.Fatalf("deleted %d", n)
	}
	s.InsertEvent(ctx, Event{AgentID: "a1", Bytes: 1, Status: "ok"})
	s.DeleteAgent(ctx, "a1")
	if got, _ := s.ListEvents(ctx, "a1", 0, 100); len(got) != 0 {
		t.Fatalf("cascade delete failed: %d", len(got))
	}
}

func TestPendingUpgradeAndPolicyRange(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.CreateAgent(ctx, Agent{ID: "a1", Name: "n", Token: "t", Secret: "x"}); err != nil {
		t.Fatal(err)
	}

	// pending upgrade set / clear
	if err := s.SetPendingUpgrade(ctx, "a1", "v0.4.0"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAgent(ctx, "a1"); got.PendingUpgrade != "v0.4.0" {
		t.Fatalf("pending: %q", got.PendingUpgrade)
	}
	if err := s.ClearPendingUpgrade(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAgent(ctx, "a1"); got.PendingUpgrade != "" {
		t.Fatalf("pending not cleared: %q", got.PendingUpgrade)
	}

	// policy random ranges persist (size + interval)
	if err := s.UpsertPolicy(ctx, Policy{AgentID: "a1", Enabled: true, IntervalSec: 60, IntervalMinSec: 30, IntervalMaxSec: 90, SizeMB: 5, SizeMinMB: 2, SizeMaxMB: 8}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.GetPolicy(ctx, "a1")
	if p.SizeMinMB != 2 || p.SizeMaxMB != 8 {
		t.Fatalf("size range not stored: %+v", p)
	}
	if p.IntervalMinSec != 30 || p.IntervalMaxSec != 90 {
		t.Fatalf("interval range not stored: %+v", p)
	}
}
