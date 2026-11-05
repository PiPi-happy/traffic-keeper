package master

import (
	"context"
	"encoding/json"
	"log"
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
	store       *store.Store
	mux         *http.ServeMux
	envPassword string // initial password seed from MASTER_ADMIN_PASSWORD
	sessions    *sessionStore
	baseURL     string // public base URL used to render install commands
	tunnel      *TunnelManager
	version     string // master/agent release version (for self-upgrade target)
	listenPort  int
}

// Option configures a Server.
type Option func(*Server)

// WithAdminPassword sets the initial admin password (used only on first run to
// seed the DB; afterwards the password is managed in the panel).
func WithAdminPassword(p string) Option {
	return func(s *Server) { s.envPassword = p }
}

// WithBaseURL sets the public base URL used when generating agent install commands.
func WithBaseURL(u string) Option {
	return func(s *Server) { s.baseURL = strings.TrimRight(u, "/") }
}

// WithVersion sets the release version (used as the agent self-upgrade target).
func WithVersion(v string) Option {
	return func(s *Server) { s.version = v }
}

// WithListenPort sets the port reported and managed for the panel listener.
func WithListenPort(port int) Option { return func(s *Server) { s.listenPort = port } }

// NewServer creates a master server backed by the given store.
func NewServer(s *store.Store, opts ...Option) *Server {
	srv := &Server{store: s, mux: http.NewServeMux(), sessions: newSessionStore(), tunnel: newTunnelManager()}
	for _, o := range opts {
		o(srv)
	}
	srv.routes()
	_ = srv.seedAppSources(context.Background())
	srv.startCleaners()
	return srv
}

func (s *Server) seedAppSources(ctx context.Context) error {
	// Official landing pages are disabled until a direct APK response is verified.
	return s.store.SeedAppSources(ctx, []store.AppSource{
		{ID: "wechat-android", Name: "微信 Android 官网入口", Publisher: "腾讯", URL: "https://weixin.qq.com/android", Enabled: false, Official: true, AllowedHosts: "weixin.qq.com", MaxMB: 150},
		{ID: "douyin-android", Name: "抖音 Android 官网入口", Publisher: "字节跳动", URL: "https://www.douyin.com/downloadpage/app", Enabled: false, Official: true, AllowedHosts: "www.douyin.com,douyin.com", MaxMB: 300},
		{ID: "alipay-android", Name: "支付宝 Android 官网入口", Publisher: "支付宝", URL: "https://render.alipay.com/p/s/download", Enabled: false, Official: true, AllowedHosts: "render.alipay.com", MaxMB: 200},
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/upload/", s.handleUpload) // data plane

	s.mux.HandleFunc("/api/agent/", s.handleAgent) // agent-facing dispatcher
	s.mux.HandleFunc("/api/login", s.handleLogin)
	s.mux.HandleFunc("/api/password", s.requireAdmin(s.handleChangePassword))
	s.mux.HandleFunc("/api/nodes", s.requireAdmin(s.handleNodes))
	s.mux.HandleFunc("/api/nodes/", s.requireAdmin(s.handleNode)) // node-specific dispatcher
	s.mux.HandleFunc("/api/tunnel", s.requireAdmin(s.handleTunnel))
	s.mux.HandleFunc("/api/tunnel/disable", s.requireAdmin(s.handleTunnelDisable))
	s.mux.HandleFunc("/api/tunnel/edge/test", s.requireAdmin(s.handleEdgeTest))
	s.mux.HandleFunc("/api/tunnel/edge/apply", s.requireAdmin(s.handleEdgeApply))
	s.mux.HandleFunc("/api/gh-proxy", s.requireAdmin(s.handleGhProxy))
	s.mux.HandleFunc("/api/listen-port", s.requireAdmin(s.handleListenPort))
	s.mux.HandleFunc("/api/dashboard", s.requireAdmin(s.handleDashboard))
	s.mux.HandleFunc("/api/automation", s.requireAdmin(s.handleAutomation))
	s.mux.HandleFunc("/api/automation/sources", s.requireAdmin(s.handleAutomationSources))
	s.mux.HandleFunc("/api/automation/sources/", s.requireAdmin(s.handleAutomationSource))
	s.mux.HandleFunc("/api/automation/source-test", s.requireAdmin(s.handleAutomationSourceTest))
	s.mux.HandleFunc("/api/automation/events", s.requireAdmin(s.handleAutomationEvents))
	s.mux.HandleFunc("/api/agent/automation/event", s.handleAgentAutomationEvent)

	// SPA frontend (embedded). More specific routes above take precedence.
	s.mux.Handle("/", web.Handler())
}

// ServeHTTP dispatches requests to the registered routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Stop tears down server-managed subprocesses (the cloudflared tunnel) so they
// exit cleanly instead of being SIGKILLed by the cgroup on process exit — a
// hard kill leaves half-dead connections that agents read as EOF during the
// restart window.
func (s *Server) Stop() {
	s.tunnel.Disable()
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

// --- background cleaners ---

const eventRetention = 3 * 24 * time.Hour

func (s *Server) startCleaners() {
	go s.cleanUploadEvents()
	go s.cleanAutomationEvents()
	// Self-heal loop watches for a cloudflared that's alive but rejected by
	// the CF edge (quick tunnel deregistered) and restarts it for a fresh
	// URL. context.Background(): tied to the process, not any request.
	go s.tunnel.SelfHealLoop(context.Background())
	// Upload-health sentinel: flag online agents that stopped uploading.
	go s.watchUploadHealth()
}

func (s *Server) cleanAutomationEvents() {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	clean := func() {
		n, err := s.store.DeleteAutomationEventsBefore(context.Background(), time.Now().Add(-30*24*time.Hour).Unix())
		if err != nil {
			log.Printf("purge automation events: %v", err)
		} else if n > 0 {
			log.Printf("purged %d old automation events", n)
		}
	}
	clean()
	for range t.C {
		clean()
	}
}

func (s *Server) cleanUploadEvents() {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	s.purgeOldEvents()
	for range ticker.C {
		s.purgeOldEvents()
	}
}

func (s *Server) purgeOldEvents() {
	cutoff := time.Now().Add(-eventRetention).Unix()
	n, err := s.store.DeleteEventsBefore(context.Background(), cutoff)
	if err != nil {
		log.Printf("purge upload events: %v", err)
	} else if n > 0 {
		log.Printf("purged %d old upload events", n)
	}
}
