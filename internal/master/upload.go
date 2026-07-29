package master

import (
	"context"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

// handleUpload is the data plane.
//
//	Path: PUT/POST /upload/{agent_id}
//	Auth: Authorization: Bearer <agent_secret>
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
	start := time.Now()
	n, err := io.Copy(io.Discard, r.Body)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		s.recordEvent(agentID, n, "fail", err.Error(), durMs)
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if err := s.store.IncrUpload(r.Context(), agentID, n); err != nil {
		s.recordEvent(agentID, n, "fail", err.Error(), durMs)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.recordEvent(agentID, n, "ok", "", durMs)

	// Minimal response: 2 bytes. No JSON, no echo.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// recordEvent inserts an upload event, logging (not failing) on DB error.
func (s *Server) recordEvent(agentID string, bytes int64, status, errMsg string, durMs int64) {
	if err := s.store.InsertEvent(context.Background(), store.Event{
		AgentID: agentID, Bytes: bytes, Status: status, Error: errMsg, DurationMs: durMs,
	}); err != nil {
		log.Printf("record upload event: %v", err)
	}
}
