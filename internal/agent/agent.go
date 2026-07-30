// Package agent implements the traffic-keeper agent.
//
// One agent can fan out to multiple masters ("一发多收"): each master has its
// own credentials + policy + upload loop. A CLI (`add`/`list`/`remove`/`stop`/
// `start`) mutates a state file; the running daemon picks up changes on SIGHUP.
package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Intervals are vars so tests can shrink them.
var (
	heartbeatInterval  = 30 * time.Second
	policyPullInterval = 30 * time.Second
	tickInterval       = 10 * time.Second
	defaultTimeout     = 5 * time.Minute
)

const upgradeRepo = "PiPi-happy/traffic-keeper"

// Config configures the agent supervisor.
type Config struct {
	State   string // state file path (all subcommands)
	Version string // build version (run + self-upgrade)
	Server  string // optional: only used to migrate a legacy v1 state file
}

// policy mirrors the master policy the agent pulls (per-master).
type policy struct {
	Enabled     bool   `json:"enabled"`
	IntervalSec int    `json:"interval_sec"`
	SizeMB      int    `json:"size_mb"`
	SizeMinMB   int    `json:"size_min_mb"`
	SizeMaxMB   int    `json:"size_max_mb"`
	UploadURL   string `json:"upload_url"`
}

// StateMaster is one master's persisted credentials.
type StateMaster struct {
	Server  string `json:"server"`            // normalized scheme://host[:port], dedup key
	AgentID string `json:"agent_id"`
	Secret  string `json:"secret"`
	Stopped bool   `json:"stopped,omitempty"`
}

// StateFile is the on-disk state (version 2 = master list).
type StateFile struct {
	Version int           `json:"version"`
	Masters []StateMaster `json:"masters"`
}

// masterConn holds one master's runtime state.
type masterConn struct {
	server  string // normalized, immutable
	agentID string
	secret  string
	stopped bool

	mu             sync.Mutex
	policy         policy
	lastHeartbeat  time.Time
	lastPolicyPull time.Time
	lastUpload     time.Time
}

// Agent is the supervisor: manages N masterConn, each with its own loop group.
type Agent struct {
	cfg    Config
	client *http.Client

	stateMu sync.Mutex
	file    StateFile

	runMu     sync.Mutex
	masters   map[string]*masterConn
	cancelers map[string]context.CancelFunc

	upgradingMu sync.Mutex
	upgrading   bool
	country     string
}

// New creates an agent supervisor.
func New(cfg Config) *Agent {
	return &Agent{
		cfg:       cfg,
		client:    &http.Client{Timeout: defaultTimeout},
		masters:   map[string]*masterConn{},
		cancelers: map[string]context.CancelFunc{},
	}
}

// Run loads state, detects country, then reconciles master loops. It reloads
// (add/remove/stop/start) on SIGHUP. Returns when ctx is canceled.
func (a *Agent) Run(ctx context.Context) error {
	if a.cfg.State == "" {
		return fmt.Errorf("state path is required")
	}
	if err := a.loadState(); err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	a.country = detectCountry()
	if a.country != "" {
		log.Printf("detected country: %s", a.country)
	}

	hupCh := make(chan os.Signal, 1)
	signal.Notify(hupCh, syscall.SIGHUP)
	go func() {
		for range hupCh {
			log.Printf("SIGHUP: reconciling masters")
			a.reconcile(ctx)
		}
	}()

	a.reconcile(ctx)
	<-ctx.Done()
	return nil
}

// reconcile re-reads the state file and starts/stops master loops to match.
// Idempotent; the SIGHUP entry point (CLI add/remove/stop/start pokes it).
func (a *Agent) reconcile(parent context.Context) {
	sf, err := LoadStateFile(a.cfg.State)
	if err != nil {
		log.Printf("reconcile: load state: %v", err)
		return
	}
	desired := map[string]StateMaster{}
	for _, m := range sf.Masters {
		if srv := NormalizeServer(m.Server); srv != "" {
			desired[srv] = m
		}
	}

	a.runMu.Lock()
	defer a.runMu.Unlock()

	// upsert: new masters are added; existing ones get refreshed credentials/stopped.
	for srv, m := range desired {
		c, ok := a.masters[srv]
		if !ok {
			c = &masterConn{server: srv}
			a.masters[srv] = c
		}
		c.agentID = m.AgentID
		c.secret = m.Secret
		c.stopped = m.Stopped
	}
	// drop masters no longer in the file.
	for srv := range a.masters {
		if _, ok := desired[srv]; !ok {
			if cancel, ok := a.cancelers[srv]; ok {
				cancel()
				delete(a.cancelers, srv)
			}
			delete(a.masters, srv)
			log.Printf("master %s: removed", srv)
		}
	}
	// start/stop to match the desired run state.
	for srv, c := range a.masters {
		_, running := a.cancelers[srv]
		shouldRun := !c.stopped && c.agentID != ""
		if shouldRun && !running {
			a.startMaster(parent, c)
		} else if !shouldRun && running {
			a.cancelers[srv]()
			delete(a.cancelers, srv)
			log.Printf("master %s: stopped", srv)
		}
	}
}

func (a *Agent) startMaster(parent context.Context, c *masterConn) {
	ctx, cancel := context.WithCancel(parent)
	a.cancelers[c.server] = cancel
	c.heartbeat(ctx, a.client, a.cfg.Version, a.country) // warm-up
	c.refreshPolicy(ctx, a.client)
	go a.heartbeatLoop(ctx, c)
	go a.policyLoop(ctx, c)
	go a.uploadLoop(ctx, c)
	log.Printf("master %s: started (agent %s)", c.server, c.agentID)
}

func (a *Agent) heartbeatLoop(ctx context.Context, c *masterConn) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			to, url, ok := c.heartbeat(ctx, a.client, a.cfg.Version, a.country)
			if ok && to != "" && to != a.cfg.Version {
				a.maybeSelfUpgrade(to, url)
			}
		}
	}
}

func (a *Agent) policyLoop(ctx context.Context, c *masterConn) {
	ticker := time.NewTicker(policyPullInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refreshPolicy(ctx, a.client)
		}
	}
}

func (a *Agent) uploadLoop(ctx context.Context, c *masterConn) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			p := c.policy
			last := c.lastUpload
			c.mu.Unlock()
			if !p.Enabled || p.IntervalSec <= 0 {
				continue
			}
			if time.Since(last) < time.Duration(p.IntervalSec)*time.Second {
				continue
			}
			c.upload(ctx, a.client)
		}
	}
}

// --- masterConn methods (cloned from the old single-master Agent) ---

func (c *masterConn) heartbeat(ctx context.Context, client *http.Client, version, country string) (upgradeTo, downloadURL string, ok bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server+"/api/agent/"+c.agentID+"/heartbeat", nil)
	if err != nil {
		log.Printf("[%s] heartbeat: %v", c.server, err)
		return "", "", false
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	if version != "" {
		req.Header.Set("X-Agent-Version", version)
	}
	req.Header.Set("X-Agent-Country", country)
	req.Header.Set("X-Agent-Arch", runtime.GOARCH)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[%s] heartbeat: %v", c.server, err)
		return "", "", false
	}
	defer resp.Body.Close()
	var hr struct {
		OK          bool   `json:"ok"`
		UpgradeTo   string `json:"upgrade_to"`
		DownloadURL string `json:"download_url"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&hr)
	if resp.StatusCode == http.StatusOK {
		c.mu.Lock()
		c.lastHeartbeat = time.Now()
		c.mu.Unlock()
		return hr.UpgradeTo, hr.DownloadURL, true
	}
	log.Printf("[%s] heartbeat: status %d", c.server, resp.StatusCode)
	return "", "", false
}

func (c *masterConn) refreshPolicy(ctx context.Context, client *http.Client) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.server+"/api/agent/"+c.agentID+"/policy", nil)
	if err != nil {
		log.Printf("[%s] policy: %v", c.server, err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[%s] policy: %v", c.server, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[%s] policy: status %d", c.server, resp.StatusCode)
		return
	}
	var p policy
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		log.Printf("[%s] policy decode: %v", c.server, err)
		return
	}
	c.mu.Lock()
	c.policy = p
	c.lastPolicyPull = time.Now()
	c.mu.Unlock()
}

func (c *masterConn) upload(ctx context.Context, client *http.Client) {
	c.mu.Lock()
	p := c.policy
	c.mu.Unlock()

	sizeMB := p.SizeMB
	if p.SizeMaxMB > p.SizeMinMB {
		sizeMB = p.SizeMinMB + mrand.IntN(p.SizeMaxMB-p.SizeMinMB+1)
	}
	if sizeMB <= 0 {
		sizeMB = 1
	}

	base := p.UploadURL
	if base == "" {
		base = c.server
	}
	n := int64(sizeMB) * 1024 * 1024
	target := strings.TrimRight(base, "/") + "/upload/" + c.agentID

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, io.LimitReader(rand.Reader, n))
	if err != nil {
		log.Printf("[%s] upload: %v", target, err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = n

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[%s] upload: %v", target, err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusOK {
		c.mu.Lock()
		c.lastUpload = time.Now()
		c.mu.Unlock()
		log.Printf("[%s] uploaded %d bytes (%dMB) in %s", target, n, sizeMB, time.Since(start).Round(time.Millisecond))
	} else {
		log.Printf("[%s] upload: status %d", target, resp.StatusCode)
	}
}

// --- self-upgrade (global, single binary) ---

func (a *Agent) maybeSelfUpgrade(target, downloadURL string) {
	a.upgradingMu.Lock()
	if a.upgrading {
		a.upgradingMu.Unlock()
		return
	}
	a.upgrading = true
	a.upgradingMu.Unlock()
	go a.selfUpgrade(target, downloadURL)
}

func (a *Agent) selfUpgrade(target, downloadURL string) {
	defer func() {
		a.upgradingMu.Lock()
		a.upgrading = false
		a.upgradingMu.Unlock()
	}()

	exe, err := os.Executable()
	if err != nil {
		log.Printf("self-upgrade: %v", err)
		return
	}
	if downloadURL == "" {
		arch := "amd64"
		if runtime.GOARCH == "arm64" {
			arch = "arm64"
		}
		downloadURL = fmt.Sprintf("https://gh-proxy.org/https://github.com/%s/releases/download/%s/traffic-keeper-agent-linux-%s", upgradeRepo, target, arch)
	}
	log.Printf("self-upgrade → %s: downloading %s", target, downloadURL)

	tmp := exe + ".new"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, downloadURL, nil)
	if err != nil {
		log.Printf("self-upgrade: %v", err)
		return
	}
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("self-upgrade download: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("self-upgrade: download status %d", resp.StatusCode)
		return
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		log.Printf("self-upgrade: %v", err)
		return
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		log.Printf("self-upgrade write: %v", err)
		return
	}
	_ = f.Close()
	if info, err := os.Stat(tmp); err != nil || info.Size() < 1<<20 {
		_ = os.Remove(tmp)
		log.Printf("self-upgrade: downloaded file too small, aborted")
		return
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		log.Printf("self-upgrade replace: %v", err)
		return
	}
	log.Printf("self-upgrade: binary replaced, restarting service...")
	if err := exec.Command("systemctl", "restart", "traffic-keeper-agent").Start(); err != nil {
		log.Printf("self-upgrade: systemctl restart failed (%v); exiting for restart", err)
		os.Exit(0)
	}
}

// --- shared registration helper (used by masterConn + CLI add) ---

// RegisterWithMaster trades a one-time install token for {agent_id, secret}.
func RegisterWithMaster(ctx context.Context, client *http.Client, server, token string) (agentID, secret string, err error) {
	body, _ := json.Marshal(map[string]string{"token": token})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/agent/register", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("register rejected: %d %s", resp.StatusCode, b)
	}
	var r struct {
		AgentID string `json:"agent_id"`
		Secret  string `json:"secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", "", err
	}
	if r.AgentID == "" || r.Secret == "" {
		return "", "", fmt.Errorf("register returned empty credentials")
	}
	return r.AgentID, r.Secret, nil
}

// --- state (v2 list + v1 migration + atomic write) ---

// loadState reads the state file, migrating a legacy v1 file if needed, and
// populates a.masters. A migrated v1 file is rewritten as v2 immediately.
func (a *Agent) loadState() error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	b, err := os.ReadFile(a.cfg.State)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	// Try v2 first.
	var sf StateFile
	if err := json.Unmarshal(b, &sf); err == nil && sf.Version == 2 {
		a.file = sf
	} else {
		// Try v1: {agent_id, secret}.
		var v1 struct {
			AgentID string `json:"agent_id"`
			Secret  string `json:"secret"`
		}
		if err := json.Unmarshal(b, &v1); err != nil {
			return fmt.Errorf("parse state: %w", err)
		}
		if v1.AgentID == "" {
			return nil // empty/unusable
		}
		srv := NormalizeServer(a.cfg.Server)
		if srv == "" {
			log.Printf("state migration: legacy v1 credential found but no --server to bind it to; skipping (re-add this master)")
		} else {
			a.file = StateFile{Version: 2, Masters: []StateMaster{{Server: srv, AgentID: v1.AgentID, Secret: v1.Secret}}}
		}
	}

	a.masters = map[string]*masterConn{}
	for _, m := range a.file.Masters {
		srv := NormalizeServer(m.Server)
		if srv == "" {
			continue
		}
		a.masters[srv] = &masterConn{server: srv, agentID: m.AgentID, secret: m.Secret, stopped: m.Stopped}
	}

	// Persist migrated v2 (best effort).
	if a.file.Version == 2 && (len(a.file.Masters) > 0 || a.cfg.Server != "") {
		_ = writeFile(a.cfg.State, a.file)
	}
	return nil
}

// detectCountry returns the agent's ISO country code via ip-api.com.
func detectCountry() string {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://ip-api.com/json/?fields=countryCode")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var d struct {
		CountryCode string `json:"countryCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(d.CountryCode))
}

// --- package-level state helpers (used by the CLI subcommands too) ---

// LoadStateFile reads a StateFile from disk (no migration side effects).
func LoadStateFile(path string) (StateFile, error) {
	var sf StateFile
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return sf, nil
	}
	if err != nil {
		return sf, err
	}
	if err := json.Unmarshal(b, &sf); err != nil {
		return sf, err
	}
	return sf, nil
}

// SaveStateFile atomically writes a StateFile to disk.
func SaveStateFile(path string, sf StateFile) error {
	return writeFile(path, sf)
}

// MasterInfo is a summary of a master for the `list` command.
type MasterInfo struct {
	Server  string
	AgentID string
	Stopped bool
}

// AddMaster registers (server, token) and upserts into the state file — the
// same normalized server overwrites credentials, a new server appends. Returns
// the agent id.
func AddMaster(ctx context.Context, path, server, token string) (string, error) {
	srv := NormalizeServer(server)
	if srv == "" {
		return "", fmt.Errorf("invalid server URL")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	id, secret, err := RegisterWithMaster(ctx, client, srv, token)
	if err != nil {
		return "", err
	}
	sf, _ := LoadStateFile(path)
	sf.Version = 2
	found := false
	for i := range sf.Masters {
		if NormalizeServer(sf.Masters[i].Server) == srv {
			sf.Masters[i].AgentID = id
			sf.Masters[i].Secret = secret
			sf.Masters[i].Stopped = false
			found = true
			break
		}
	}
	if !found {
		sf.Masters = append(sf.Masters, StateMaster{Server: srv, AgentID: id, Secret: secret})
	}
	if err := SaveStateFile(path, sf); err != nil {
		return "", err
	}
	return id, nil
}

// RemoveMaster deletes a master from the state file. Returns found=false if absent.
func RemoveMaster(path, server string) (found bool, err error) {
	srv := NormalizeServer(server)
	sf, err := LoadStateFile(path)
	if err != nil {
		return false, err
	}
	kept := sf.Masters[:0]
	for _, m := range sf.Masters {
		if NormalizeServer(m.Server) == srv {
			found = true
			continue
		}
		kept = append(kept, m)
	}
	sf.Masters = kept
	if !found {
		return false, nil
	}
	return true, SaveStateFile(path, sf)
}

// SetMasterStopped toggles a master's stopped flag. Returns found=false if absent.
func SetMasterStopped(path, server string, stopped bool) (found bool, err error) {
	srv := NormalizeServer(server)
	sf, err := LoadStateFile(path)
	if err != nil {
		return false, err
	}
	for i := range sf.Masters {
		if NormalizeServer(sf.Masters[i].Server) == srv {
			sf.Masters[i].Stopped = stopped
			return true, SaveStateFile(path, sf)
		}
	}
	return false, nil
}

// ListMasters returns all masters in the state file.
func ListMasters(path string) ([]MasterInfo, error) {
	sf, err := LoadStateFile(path)
	if err != nil {
		return nil, err
	}
	out := make([]MasterInfo, 0, len(sf.Masters))
	for _, m := range sf.Masters {
		out = append(out, MasterInfo{Server: m.Server, AgentID: m.AgentID, Stopped: m.Stopped})
	}
	return out, nil
}

// Reload pokes the running agent (systemd ExecReload → SIGHUP → reconcile).
// Failures are ignored: the state file is already updated, so the next start
// picks up the change.
func Reload() {
	_ = exec.Command("systemctl", "reload", "traffic-keeper-agent").Run()
}

func writeFile(path string, sf StateFile) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// NormalizeServer canonicalizes a server URL (scheme://host[:port], no trailing slash).
func NormalizeServer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.TrimRight(s, "/")
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		// not a valid absolute URL; return trimmed as-is if it has a scheme
		if strings.Contains(s, "://") {
			return s
		}
		return ""
	}
	return u.Scheme + "://" + u.Host
}
