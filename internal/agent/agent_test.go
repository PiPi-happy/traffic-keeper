package agent

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PiPi-happy/traffic-keeper/internal/master"
	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

// startFakeMaster stands up a real master.Server (with a pre-registered node)
// so the agent can exercise the full register → heartbeat → policy → upload
// flow end-to-end.
func startFakeMaster(t *testing.T, token string) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateAgent(context.Background(), store.Agent{ID: "a1", Name: "n1", Token: token, Secret: "sec_test"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	hs := httptest.NewServer(master.NewServer(st))
	t.Cleanup(hs.Close)
	return hs, st
}

func TestAgentEndToEnd(t *testing.T) {
	hs, st := startFakeMaster(t, "tok_test")
	ctx := context.Background()
	statePath := filepath.Join(t.TempDir(), "agent.state.json")

	a := New(Config{Server: hs.URL, Token: "tok_test", State: statePath})

	// register trades the install token for credentials
	if err := a.register(ctx); err != nil {
		t.Fatalf("register: %v", err)
	}
	if a.state.AgentID != "a1" || a.state.Secret != "sec_test" {
		t.Fatalf("credentials: %+v", a.state)
	}

	// credentials persist across restarts (no token needed next time)
	a2 := New(Config{Server: hs.URL, State: statePath})
	if err := a2.loadState(); err != nil || a2.state.AgentID != "a1" {
		t.Fatalf("persisted state: %v %+v", err, a2.state)
	}

	// policy pull returns the defaults seeded by the master
	a.refreshPolicy(ctx)
	if !a.policy.Enabled || a.policy.IntervalSec != 1800 || a.policy.SizeMB != 50 {
		t.Fatalf("policy: %+v", a.policy)
	}

	// heartbeat updates last_seen server-side
	a.heartbeat(ctx)
	ag, _ := st.GetAgent(ctx, "a1")
	if ag.LastSeenAt == 0 {
		t.Fatal("heartbeat not recorded")
	}

	// uploads are counted server-side (2 × 1 MiB)
	a.policy.SizeMB = 1
	a.upload(ctx)
	a.upload(ctx)
	stats, _ := st.GetStats(ctx, "a1")
	if stats.BytesUp != 2<<20 || stats.UploadCount != 2 {
		t.Fatalf("stats after 2 uploads: %+v", stats)
	}
}
