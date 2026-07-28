package master

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
	"github.com/PiPi-happy/traffic-keeper/internal/web"
)

// Server is the master HTTP server. It hosts both the control plane (node
// management, policy dispatch, single-user auth) and the data plane (upload
// receiver that counts bytes and discards bodies).
type Server struct {
	store         *store.Store
	mux           *http.ServeMux
	adminPassword string
	sessions      *sessionStore
	baseURL       string // public base URL used to render install commands
}

// Option configures a Server.
type Option func(*Server)

// WithAdminPassword sets the single-user admin password for panel login.
func WithAdminPassword(p string) Option {
	return func(s *Server) { s.adminPassword = p }
}

// WithBaseURL sets the public base URL (e.g. https://master.example.com) used
// when generating agent install commands.
func WithBaseURL(u string) Option {
	return func(s *Server) { s.baseURL = strings.TrimRight(u, "/") }
}

// NewServer creates a master server backed by the given store.
func NewServer(s *store.Store, opts ...Option) *Server {
	srv := &Server{store: s, mux: http.NewServeMux(), sessions: newSessionStore()}
	for _, o := range opts {
		o(srv)
	}
	srv.routes()
	return srv
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/upload/", s.handleUpload) // data plane

	s.mux.HandleFunc("/api/agent/", s.handleAgent)              // agent-facing dispatcher
	s.mux.HandleFunc("/api/login", s.handleLogin)
	s.mux.HandleFunc("/api/nodes", s.requireAdmin(s.handleNodes))
	s.mux.HandleFunc("/api/nodes/", s.requireAdmin(s.handleNode)) // node-specific dispatcher

	// SPA frontend (embedded). More specific routes above take precedence.
	s.mux.Handle("/", web.Handler())
}

// ServeHTTP dispatches requests to the registered routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// writeJSON encodes v as JSON with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// clientIP returns the client's IP, honoring X-Forwarded-For (set by Caddy).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host
}

// isOnline returns true if the agent heartbeated within the last 90s.
func isOnline(lastSeen int64) bool {
	if lastSeen == 0 {
		return false
	}
	return time.Now().Unix()-lastSeen <= 90
}
