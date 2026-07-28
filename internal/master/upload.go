package master

import (
	"context"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

// handleUpload is the data plane.
//
//	Path: PUT/POST /upload/{agent_id}
//	Auth: Authorization: Bearer <agent_secret>
//
// It authenticates the agent, streams the request body into io.Discard while
// counting the bytes, increments the agent's upstream counter, records an
// upload event, and returns a minimal "ok" body. The request body is never
// stored or echoed back, so the downstream (download) traffic on the agent
// stays near zero.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentID := strings.TrimPrefix(r.URL.Path, "/upload/")
	if agentID == "" || strings.Contains(agentID, "/") {
		http.Error(w, "invalid agent id", http.StatusBadRequest)
		return
	}

	secret := bearerToken(r)
	if secret == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}

	agent, err := s.store.GetAgent(r.Context(), agentID)
	if err != nil {
		http.Error(w, "unknown agent", http.StatusUnauthorized)
		return
	}
	if agent.Secret != secret {
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}
	if !agent.Enabled {
		http.Error(w, "agent disabled", http.StatusForbidden)
		return
	}

	// Stream the body to /dev/null while counting. Never load it into memory.
	n, err := io.Copy(io.Discard, r.Body)
	if err != nil {
		s.recordEvent(agentID, n, "fail", err.Error())
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if err := s.store.IncrUpload(r.Context(), agentID, n); err != nil {
		s.recordEvent(agentID, n, "fail", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.recordEvent(agentID, n, "ok", "")

	// Minimal response: 2 bytes. No JSON, no echo.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// recordEvent inserts an upload event, logging (not failing) on DB error so a
// stats hiccup never breaks the data-plane response.
func (s *Server) recordEvent(agentID string, bytes int64, status, errMsg string) {
	if err := s.store.InsertEvent(context.Background(), store.Event{AgentID: agentID, Bytes: bytes, Status: status, Error: errMsg}); err != nil {
		log.Printf("record upload event: %v", err)
	}
}
