package master

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

// installScriptURL is the public raw URL of the agent installer.
const installScriptURL = "https://raw.githubusercontent.com/PiPi-happy/traffic-keeper/main/deploy/install.sh"

type loginReq struct {
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.verifyAdmin(r.Context(), req.Password) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": s.sessions.create()})
}

type passwordReq struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// handleChangePassword verifies the old password then stores a new bcrypt hash.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req passwordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.verifyAdmin(r.Context(), req.Old) {
		http.Error(w, "old password incorrect", http.StatusUnauthorized)
		return
	}
	if len(req.New) < 6 {
		http.Error(w, "new password must be at least 6 characters", http.StatusBadRequest)
		return
	}
	hash, err := hashPassword(req.New)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.store.SetSetting(r.Context(), settingAdminPassword, hash); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type createNodeReq struct {
	Name string `json:"name"`
}

// handleNodes handles GET (list) and POST (create) on /api/nodes.
func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listNodes(w, r)
	case http.MethodPost:
		s.createNode(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	agents, err := s.store.ListAgents(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	stats, _ := s.store.AllStats(r.Context())

	out := make([]map[string]any, 0, len(agents))
	for _, a := range agents {
		st := stats[a.ID]
		p, _ := s.store.GetPolicy(r.Context(), a.ID)
		out = append(out, map[string]any{
			"id":             a.ID,
			"name":           a.Name,
			"enabled":        a.Enabled,
			"created_at":     a.CreatedAt,
			"last_seen_at":   a.LastSeenAt,
			"last_ip":        a.LastIP,
			"online":         isOnline(a.LastSeenAt),
			"bytes_up":       st.BytesUp,
			"upload_count":   st.UploadCount,
			"last_upload_at": st.LastUploadAt,
			"policy": map[string]any{
				"enabled":      p.Enabled,
				"interval_sec": p.IntervalSec,
				"size_mb":      p.SizeMB,
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": out})
}

func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	var req createNodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	agent := store.Agent{
		ID:     newAgentID(),
		Name:   name,
		Token:  newToken(),
		Secret: newSecret(),
	}
	if err := s.store.CreateAgent(r.Context(), agent); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":              agent.ID,
		"name":            agent.Name,
		"token":           agent.Token,
		"install_command": s.installCommand(agent.Token),
	})
}

// handleNode dispatches node-specific routes under /api/nodes/{id}[/suffix].
func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	switch {
	case strings.HasSuffix(p, "/policy"):
		s.handleNodePolicy(w, r)
	case strings.HasSuffix(p, "/events"):
		s.handleNodeEvents(w, r)
	case strings.HasSuffix(p, "/install-command"):
		s.handleNodeInstallCmd(w, r)
	case p != "" && !strings.Contains(p, "/"):
		s.handleNodeDelete(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleNodePolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/nodes/"), "/policy")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var body struct {
		Enabled     *bool `json:"enabled"`
		IntervalSec *int  `json:"interval_sec"`
		SizeMB      *int  `json:"size_mb"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	p, err := s.store.GetPolicy(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if body.Enabled != nil {
		p.Enabled = *body.Enabled
	}
	if body.IntervalSec != nil {
		if *body.IntervalSec < 1 {
			http.Error(w, "interval_sec must be >= 1", http.StatusBadRequest)
			return
		}
		p.IntervalSec = *body.IntervalSec
	}
	if body.SizeMB != nil {
		if *body.SizeMB < 1 {
			http.Error(w, "size_mb must be >= 1", http.StatusBadRequest)
			return
		}
		p.SizeMB = *body.SizeMB
	}
	if err := s.store.UpsertPolicy(r.Context(), p); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":      p.Enabled,
		"interval_sec": p.IntervalSec,
		"size_mb":      p.SizeMB,
	})
}

// handleNodeEvents returns upload events for the last 3 days, newest first.
func (s *Server) handleNodeEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/nodes/"), "/events")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	since := time.Now().Unix() - 3*24*3600
	events, err := s.store.ListEvents(r.Context(), id, since, 1000)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(events))
	for _, e := range events {
		out = append(out, map[string]any{
			"ts":     e.Ts,
			"bytes":  e.Bytes,
			"status": e.Status,
			"error":  e.Error,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

// handleNodeInstallCmd regenerates the install command for an existing node.
func (s *Server) handleNodeInstallCmd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/nodes/"), "/install-command")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	agent, err := s.store.GetAgent(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"install_command": s.installCommand(agent.Token)})
}

func (s *Server) handleNodeDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteAgent(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// installCommand renders the one-liner the panel shows for a node.
func (s *Server) installCommand(token string) string {
	server := s.baseURL
	if server == "" {
		server = "<MASTER_URL>"
	}
	return "curl -fsSL " + installScriptURL + " | bash -s -- --token " + token + " --server " + server
}
