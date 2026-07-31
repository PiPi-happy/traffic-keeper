package master

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleAgent dispatches agent-facing control-plane routes under /api/agent/.
//
//	POST /api/agent/register
//	POST /api/agent/{id}/heartbeat
//	GET  /api/agent/{id}/policy
func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/agent/")
	switch {
	case p == "register":
		s.handleAgentRegister(w, r)
	case strings.HasSuffix(p, "/heartbeat"):
		s.handleAgentHeartbeat(w, r)
	case strings.HasSuffix(p, "/policy"):
		s.handleAgentPolicy(w, r)
	default:
		http.NotFound(w, r)
	}
}

type registerReq struct {
	Token string `json:"token"`
}

type registerResp struct {
	AgentID string `json:"agent_id"`
	Secret  string `json:"secret"`
}

func (s *Server) handleAgentRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	agent, err := s.store.GetAgentByToken(r.Context(), req.Token)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, registerResp{AgentID: agent.ID, Secret: agent.Secret})
}

func (s *Server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/agent/"), "/heartbeat")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	agent, ok := s.authenticateAgent(r, id)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	version := r.Header.Get("X-Agent-Version")
	country := r.Header.Get("X-Agent-Country")
	arch := r.Header.Get("X-Agent-Arch")
	if err := s.store.TouchAgent(r.Context(), id, clientIP(r), version, country, arch); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp := map[string]any{"ok": true}
	// Self-upgrade directive: if a target is pending and the agent isn't on it
	// yet, tell it (with a region-aware download URL); if it already is, clear.
	if agent.PendingUpgrade != "" {
		if version != "" && version == agent.PendingUpgrade {
			_ = s.store.ClearPendingUpgrade(r.Context(), id)
		} else {
			resp["upgrade_to"] = agent.PendingUpgrade
			resp["download_url"] = s.buildDownloadURL(r.Context(), agent.PendingUpgrade, arch, country)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAgentPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/agent/"), "/policy")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if _, ok := s.authenticateAgent(r, id); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	p, err := s.store.GetPolicy(r.Context(), id)
	if err != nil {
		http.Error(w, "no policy", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":          p.Enabled,
		"interval_sec":     p.IntervalSec,
		"interval_min_sec": p.IntervalMinSec,
		"interval_max_sec": p.IntervalMaxSec,
		"size_mb":          p.SizeMB,
		"size_min_mb":      p.SizeMinMB,
		"size_max_mb":      p.SizeMaxMB,
		"upload_url":       s.tunnel.UploadURL(), // tunnel base when up ("" while negotiating)
		"tunnel_enabled":   s.tunnel.Enabled(),   // whether a tunnel is intended to be up
	})
}
