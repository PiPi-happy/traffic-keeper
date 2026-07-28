package master

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

func newAPIServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewServer(st, WithAdminPassword("s3cret"), WithBaseURL("https://master.example.com"))
}

func doReq(t *testing.T, srv *Server, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req = httptest.NewRequest(method, path, bytes.NewReader(b))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func adminToken(t *testing.T, srv *Server) string {
	t.Helper()
	rec := doReq(t, srv, http.MethodPost, "/api/login", "", map[string]string{"password": "s3cret"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d", rec.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp["token"]
}

func TestLoginAndAdminAuth(t *testing.T) {
	srv := newAPIServer(t)

	if rec := doReq(t, srv, http.MethodPost, "/api/login", "", map[string]string{"password": "bad"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad password: %d", rec.Code)
	}
	if rec := doReq(t, srv, http.MethodGet, "/api/nodes", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no admin token: %d", rec.Code)
	}
	tok := adminToken(t, srv)
	if rec := doReq(t, srv, http.MethodGet, "/api/nodes", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("with admin token: %d", rec.Code)
	}
}

func TestNodeLifecycle(t *testing.T) {
	srv := newAPIServer(t)
	tok := adminToken(t, srv)

	// create node → returns id, token, install command
	rec := doReq(t, srv, http.MethodPost, "/api/nodes", tok, map[string]string{"name": "vps-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	agentID, _ := created["id"].(string)
	token, _ := created["token"].(string)
	inst, _ := created["install_command"].(string)
	if agentID == "" || token == "" || inst == "" {
		t.Fatalf("create resp missing fields: %+v", created)
	}

	// register: agent trades the install token for its secret
	rec = doReq(t, srv, http.MethodPost, "/api/agent/register", "", map[string]string{"token": token})
	if rec.Code != http.StatusOK {
		t.Fatalf("register: %d", rec.Code)
	}
	var reg map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &reg)
	secret := reg["secret"]
	if reg["agent_id"] != agentID || secret == "" {
		t.Fatalf("register resp: %+v", reg)
	}

	// register with bad token
	if rec := doReq(t, srv, http.MethodPost, "/api/agent/register", "", map[string]string{"token": "tok_bogus"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad register: %d", rec.Code)
	}

	// heartbeat with secret → ok
	if rec := doReq(t, srv, http.MethodPost, "/api/agent/"+agentID+"/heartbeat", secret, nil); rec.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d", rec.Code)
	}
	// heartbeat with wrong secret → 401
	if rec := doReq(t, srv, http.MethodPost, "/api/agent/"+agentID+"/heartbeat", "wrong", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad heartbeat: %d", rec.Code)
	}

	// pull default policy
	rec = doReq(t, srv, http.MethodGet, "/api/agent/"+agentID+"/policy", secret, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get policy: %d", rec.Code)
	}
	var pol map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &pol)
	if pol["interval_sec"].(float64) != 1800 || !pol["enabled"].(bool) {
		t.Fatalf("default policy: %+v", pol)
	}

	// admin updates policy; agent sees the change
	rec = doReq(t, srv, http.MethodPut, "/api/nodes/"+agentID+"/policy", tok, map[string]any{"enabled": false, "size_mb": 20, "interval_sec": 600})
	if rec.Code != http.StatusOK {
		t.Fatalf("update policy: %d", rec.Code)
	}
	rec = doReq(t, srv, http.MethodGet, "/api/agent/"+agentID+"/policy", secret, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &pol)
	if pol["enabled"].(bool) || pol["size_mb"].(float64) != 20 || pol["interval_sec"].(float64) != 600 {
		t.Fatalf("updated policy not reflected: %+v", pol)
	}

	// policy validation
	if rec := doReq(t, srv, http.MethodPut, "/api/nodes/"+agentID+"/policy", tok, map[string]any{"size_mb": 0}); rec.Code != http.StatusBadRequest {
		t.Fatalf("size_mb=0 should be rejected: %d", rec.Code)
	}

	// list shows 1 node, online
	rec = doReq(t, srv, http.MethodGet, "/api/nodes", tok, nil)
	var list map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	nodes, _ := list["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("nodes len: %d", len(nodes))
	}
	n0, _ := nodes[0].(map[string]any)
	if !n0["online"].(bool) {
		t.Fatal("node should be online after heartbeat")
	}

	// delete
	if rec := doReq(t, srv, http.MethodDelete, "/api/nodes/"+agentID, tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	rec = doReq(t, srv, http.MethodGet, "/api/nodes", tok, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	nodes, _ = list["nodes"].([]any)
	if len(nodes) != 0 {
		t.Fatalf("after delete nodes len: %d", len(nodes))
	}
}
