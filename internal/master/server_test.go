package master

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.CreateAgent(ctx, store.Agent{ID: "a1", Name: "n1", Token: "tok1", Secret: "sec1"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return NewServer(st), st
}

func do(srv *Server, method, path, secret string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestUploadDataPlane(t *testing.T) {
	srv, st := newTestServer(t)
	ctx := context.Background()

	payload := bytes.Repeat([]byte("x"), 4096)

	// 1. happy path: correct secret, agent enabled → 200 "ok", bytes counted.
	rec := do(srv, http.MethodPut, "/upload/a1", "sec1", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("happy: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("happy: body=%q want \"ok\"", rec.Body.String())
	}
	got, _ := st.GetStats(ctx, "a1")
	if got.BytesUp != 4096 || got.UploadCount != 1 {
		t.Fatalf("stats after upload: %+v", got)
	}

	// 2. wrong secret → 401
	if rec := do(srv, http.MethodPut, "/upload/a1", "wrong", payload); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: code=%d", rec.Code)
	}

	// 3. unknown agent → 401
	if rec := do(srv, http.MethodPut, "/upload/ghost", "sec1", payload); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown agent: code=%d", rec.Code)
	}

	// 4. disabled agent → 403
	if err := st.SetAgentEnabled(ctx, "a1", false); err != nil {
		t.Fatal(err)
	}
	if rec := do(srv, http.MethodPut, "/upload/a1", "sec1", payload); rec.Code != http.StatusForbidden {
		t.Fatalf("disabled: code=%d", rec.Code)
	}

	// 5. wrong method → 405 (checked before auth)
	if rec := do(srv, http.MethodGet, "/upload/a1", "sec1", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: code=%d", rec.Code)
	}

	// 6. stats unchanged after the failed uploads (only the happy path counted).
	got, _ = st.GetStats(ctx, "a1")
	if got.UploadCount != 1 {
		t.Fatalf("stats should be unchanged: %+v", got)
	}
}

func TestHealthz(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := do(srv, http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz: code=%d body=%q", rec.Code, rec.Body.String())
	}
}
