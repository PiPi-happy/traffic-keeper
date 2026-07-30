package agent

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PiPi-happy/traffic-keeper/internal/master"
	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

// startFakeMaster stands up a real master.Server (with a pre-registered agent
// + a fast 1s/1MB policy) so the agent can register/pull/upload against it.
func startFakeMaster(t *testing.T, token string) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.CreateAgent(ctx, store.Agent{ID: "a1", Name: "n", Token: token, Secret: "sec_test"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := st.UpsertPolicy(ctx, store.Policy{AgentID: "a1", Enabled: true, IntervalSec: 1, SizeMB: 1}); err != nil {
		t.Fatalf("seed fast policy: %v", err)
	}
	hs := httptest.NewServer(master.NewServer(st))
	t.Cleanup(hs.Close)
	return hs, st
}

func waitUpload(t *testing.T, st *store.Store, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s, err := st.GetStats(context.Background(), id); err == nil && s.UploadCount > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout: agent %s never uploaded within %s", id, timeout)
}

// TestStateMigrationV1: a legacy {agent_id,secret} file + Config.Server migrates
// to a v2 single-master list and is persisted.
func TestStateMigrationV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"agent_id":"old","secret":"s1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{State: path, Version: "test", Server: "https://m.example.com"})
	if err := a.loadState(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(a.masters) != 1 {
		t.Fatalf("expected 1 migrated master, got %d", len(a.masters))
	}
	c := a.masters["https://m.example.com"]
	if c == nil || c.agentID != "old" || c.secret != "s1" {
		t.Fatalf("migrated conn wrong: %+v", c)
	}
	sf, _ := LoadStateFile(path)
	if sf.Version != 2 || len(sf.Masters) != 1 {
		t.Fatalf("v2 not persisted: %+v", sf)
	}
}

// TestAddMasterSameServerOverwrite: adding the same server twice keeps a single
// entry (credentials overwritten), satisfying the "same master, no duplicate" rule.
func TestAddMasterSameServerOverwrite(t *testing.T) {
	m1, _ := startFakeMaster(t, "tok1")
	path := filepath.Join(t.TempDir(), "state.json")
	ctx := context.Background()
	if _, err := AddMaster(ctx, path, m1.URL, "tok1"); err != nil {
		t.Fatalf("add1: %v", err)
	}
	if _, err := AddMaster(ctx, path, m1.URL, "tok1"); err != nil { // same server, same token
		t.Fatalf("add2: %v", err)
	}
	sf, _ := LoadStateFile(path)
	if len(sf.Masters) != 1 {
		t.Fatalf("expected 1 master (overwrite), got %d: %+v", len(sf.Masters), sf.Masters)
	}
}

// TestAddMasterDifferentServersAppend: two different servers → two entries.
func TestAddMasterDifferentServersAppend(t *testing.T) {
	m1, _ := startFakeMaster(t, "tok1")
	m2, _ := startFakeMaster(t, "tok2")
	path := filepath.Join(t.TempDir(), "state.json")
	ctx := context.Background()
	if _, err := AddMaster(ctx, path, m1.URL, "tok1"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddMaster(ctx, path, m2.URL, "tok2"); err != nil {
		t.Fatal(err)
	}
	ms, _ := ListMasters(path)
	if len(ms) != 2 {
		t.Fatalf("expected 2 masters, got %d", len(ms))
	}
}

// TestMultiMasterUpload: one agent fans out to two masters, both receive uploads.
func TestMultiMasterUpload(t *testing.T) {
	heartbeatInterval = 50 * time.Millisecond
	policyPullInterval = 50 * time.Millisecond
	tickInterval = 50 * time.Millisecond

	m1, st1 := startFakeMaster(t, "tok1")
	m2, st2 := startFakeMaster(t, "tok2")

	path := filepath.Join(t.TempDir(), "state.json")
	ctx := context.Background()
	if _, err := AddMaster(ctx, path, m1.URL, "tok1"); err != nil {
		t.Fatalf("add m1: %v", err)
	}
	if _, err := AddMaster(ctx, path, m2.URL, "tok2"); err != nil {
		t.Fatalf("add m2: %v", err)
	}

	a := New(Config{State: path, Version: "test"})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = a.Run(runCtx) }()

	waitUpload(t, st1, "a1", 5*time.Second)
	waitUpload(t, st2, "a1", 5*time.Second)
}

// TestStopRemove: stop pauses one master (other keeps uploading); remove drops it.
func TestStopRemove(t *testing.T) {
	heartbeatInterval = 50 * time.Millisecond
	policyPullInterval = 50 * time.Millisecond
	tickInterval = 50 * time.Millisecond

	m1, st1 := startFakeMaster(t, "tok1")
	m2, st2 := startFakeMaster(t, "tok2")

	path := filepath.Join(t.TempDir(), "state.json")
	ctx := context.Background()
	AddMaster(ctx, path, m1.URL, "tok1")
	AddMaster(ctx, path, m2.URL, "tok2")

	a := New(Config{State: path, Version: "test"})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = a.Run(runCtx) }()

	waitUpload(t, st1, "a1", 5*time.Second)
	waitUpload(t, st2, "a1", 5*time.Second)

	// stop m1 (in-process reconcile), m2 keeps growing.
	a.reconcile(runCtx) // ensure loops started
	if found, _ := SetMasterStopped(path, m1.URL, true); !found {
		t.Fatal("stop: m1 not found")
	}
	a.reconcile(runCtx)

	before2, _ := st2.GetStats(ctx, "a1")
	// m2 has a 1s interval; poll up to 3s for another upload (proving it keeps
	// running after m1 is stopped).
	dl := time.Now().Add(3 * time.Second)
	var after2 store.Stats
	for time.Now().Before(dl) {
		after2, _ = st2.GetStats(ctx, "a1")
		if after2.UploadCount > before2.UploadCount {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if after2.UploadCount <= before2.UploadCount {
		t.Fatalf("m2 should keep uploading after stopping m1: before=%d after=%d", before2.UploadCount, after2.UploadCount)
	}

	// remove m2 → state has only m1.
	if found, _ := RemoveMaster(path, m2.URL); !found {
		t.Fatal("remove: m2 not found")
	}
	ms, _ := ListMasters(path)
	if len(ms) != 1 || NormalizeServer(ms[0].Server) != NormalizeServer(m1.URL) {
		t.Fatalf("after remove expected only m1, got %+v", ms)
	}
}

// TestNormalizeServer: dedup key is scheme://host[:port], trailing slash trimmed.
func TestNormalizeServer(t *testing.T) {
	cases := map[string]string{
		"https://m.example.com":      "https://m.example.com",
		"https://m.example.com/":     "https://m.example.com",
		"http://1.2.3.4:8080/x/y":    "http://1.2.3.4:8080",
		"https://x.trycloudflare.com": "https://x.trycloudflare.com",
		"":                           "",
		"not-a-url":                  "",
	}
	for in, want := range cases {
		if got := NormalizeServer(in); got != want {
			t.Errorf("NormalizeServer(%q)=%q want %q", in, got, want)
		}
	}
}
