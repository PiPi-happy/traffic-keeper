package master

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionTTL            = 24 * time.Hour
	settingAdminPassword  = "admin_password_hash"
	settingTunnelEnabled  = "tunnel_enabled"
	settingGhProxy        = "gh_proxy"
	settingTunnelEdgeMode = "tunnel_edge_mode" // "off" | "auto" | "manual"
	settingTunnelEdgeIP   = "tunnel_edge_ip"   // manual/auto: applied edge IP
	settingCFCIDRs        = "cf_cidrs_cache"   // cached cloudflare.com/ips-v4
)

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand should never fail
	}
	return hex.EncodeToString(b)
}

func newAgentID() string    { return randomHex(16) }
func newToken() string      { return "tok_" + randomHex(16) }
func newSecret() string     { return "sec_" + randomHex(24) }
func newAdminToken() string { return "adm_" + randomHex(24) }

// sessionStore holds short-lived admin session tokens in memory.
type sessionStore struct {
	mu   sync.Mutex
	sess map[string]time.Time // token -> expiry
}

func newSessionStore() *sessionStore {
	return &sessionStore{sess: make(map[string]time.Time)}
}

func (s *sessionStore) create() string {
	tok := newAdminToken()
	s.mu.Lock()
	s.sess[tok] = time.Now().Add(sessionTTL)
	s.mu.Unlock()
	return tok
}

func (s *sessionStore) valid(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sess[tok]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.sess, tok)
		return false
	}
	return true
}

// bearerToken extracts a Bearer token from the Authorization header.
func bearerToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if strings.HasPrefix(v, prefix) {
		return strings.TrimPrefix(v, prefix)
	}
	return ""
}

// authenticateAgent loads the agent by id and verifies its Bearer secret.
func (s *Server) authenticateAgent(r *http.Request, id string) (store.Agent, bool) {
	secret := bearerToken(r)
	if secret == "" {
		return store.Agent{}, false
	}
	agent, err := s.store.GetAgent(r.Context(), id)
	if err != nil || agent.Secret != secret {
		return store.Agent{}, false
	}
	return agent, true
}

// requireAdmin wraps an admin-only handler.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if tok == "" || !s.sessions.valid(tok) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// --- admin password (bcrypt, persisted in settings) ---

func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func verifyPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// InitAdminPassword seeds the admin password hash from envPassword on first run.
// Once seeded, the password is managed in the DB (panel can change it); the env
// var is only an initial value.
func (s *Server) InitAdminPassword(ctx context.Context, envPassword string) error {
	if _, err := s.store.GetSetting(ctx, settingAdminPassword); err == nil {
		return nil // already configured
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	pw := envPassword
	if pw == "" {
		pw = randomHex(12) // generate a random one and log it once
	}
	h, err := hashPassword(pw)
	if err != nil {
		return err
	}
	if err := s.store.SetSetting(ctx, settingAdminPassword, h); err != nil {
		return err
	}
	if envPassword == "" {
		log.Printf("no MASTER_ADMIN_PASSWORD set; generated initial admin password: %s", pw)
	}
	return nil
}

// verifyAdmin checks the password against the stored bcrypt hash.
func (s *Server) verifyAdmin(ctx context.Context, pw string) bool {
	h, err := s.store.GetSetting(ctx, settingAdminPassword)
	if err != nil {
		return false
	}
	return verifyPassword(h, pw)
}
