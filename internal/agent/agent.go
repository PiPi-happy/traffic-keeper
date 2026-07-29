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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	heartbeatInterval  = 30 * time.Second
	policyPullInterval = 30 * time.Second
	tickInterval       = 10 * time.Second
	defaultTimeout     = 5 * time.Minute
)

// upgradeRepo is the GitHub repo releases are published to.
const upgradeRepo = "PiPi-happy/traffic-keeper"

// Config configures the agent.
type Config struct {
	Server  string
	Token   string
	State   string
	Version string // build version, reported to master and used in self-upgrade checks
}

// policy mirrors the master policy the agent pulls.
type policy struct {
	Enabled     bool   `json:"enabled"`
	IntervalSec int    `json:"interval_sec"`
	SizeMB      int    `json:"size_mb"`
	SizeMinMB   int    `json:"size_min_mb"`
	SizeMaxMB   int    `json:"size_max_mb"`
	UploadURL   string `json:"upload_url"`
}

// state is the persisted credential pair.
type state struct {
	AgentID string `json:"agent_id"`
	Secret  string `json:"secret"`
}

// Agent is a traffic-keeper sender.
type Agent struct {
	cfg    Config
	client *http.Client
	state  state

	mu             sync.Mutex
	policy         policy
	lastHeartbeat  time.Time
	lastPolicyPull time.Time
	lastUpload     time.Time
	upgrading      bool
}

// New creates an agent.
func New(cfg Config) *Agent {
	return &Agent{
		cfg:    cfg,
		client: &http.Client{Timeout: defaultTimeout},
		policy: policy{Enabled: true, IntervalSec: 1800, SizeMB: 50},
	}
}

// Run registers (if needed) then runs heartbeat / policy / upload each in its
// own goroutine so a slow upload can never starve heartbeats.
func (a *Agent) Run(ctx context.Context) error {
	if a.cfg.Server == "" {
		return fmt.Errorf("server URL is required")
	}
	if err := a.loadState(); err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if a.state.AgentID == "" {
		if a.cfg.Token == "" {
			return fmt.Errorf("no saved credentials and no token to register")
		}
		if err := a.register(ctx); err != nil {
			return fmt.Errorf("register: %w", err)
		}
		log.Printf("registered as agent %s", a.state.AgentID)
	} else {
		log.Printf("resuming agent %s", a.state.AgentID)
	}

	a.heartbeat(ctx)
	a.refreshPolicy(ctx)

	go a.heartbeatLoop(ctx)
	go a.policyLoop(ctx)
	go a.uploadLoop(ctx)

	<-ctx.Done()
	return nil
}

func (a *Agent) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.heartbeat(ctx)
		}
	}
}

func (a *Agent) policyLoop(ctx context.Context) {
	ticker := time.NewTicker(policyPullInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.refreshPolicy(ctx)
		}
	}
}

func (a *Agent) uploadLoop(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			p := a.policy
			last := a.lastUpload
			a.mu.Unlock()
			if !p.Enabled || p.IntervalSec <= 0 {
				continue
			}
			if time.Since(last) < time.Duration(p.IntervalSec)*time.Second {
				continue
			}
			a.upload(ctx)
		}
	}
}

func (a *Agent) register(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"token": a.cfg.Token})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.Server+"/api/agent/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register rejected: %d %s", resp.StatusCode, b)
	}
	var r struct {
		AgentID string `json:"agent_id"`
		Secret  string `json:"secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return err
	}
	if r.AgentID == "" || r.Secret == "" {
		return fmt.Errorf("register returned empty credentials")
	}
	a.state = state{AgentID: r.AgentID, Secret: r.Secret}
	return a.saveState()
}

func (a *Agent) heartbeat(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.Server+"/api/agent/"+a.state.AgentID+"/heartbeat", nil)
	if err != nil {
		log.Printf("heartbeat: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+a.state.Secret)
	if a.cfg.Version != "" {
		req.Header.Set("X-Agent-Version", a.cfg.Version)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("heartbeat: %v", err)
		return
	}
	defer resp.Body.Close()
	var hr struct {
		OK        bool   `json:"ok"`
		UpgradeTo string `json:"upgrade_to"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&hr)
	if resp.StatusCode == http.StatusOK {
		a.mu.Lock()
		a.lastHeartbeat = time.Now()
		a.mu.Unlock()
		// Master told us to self-upgrade to a newer version.
		if hr.UpgradeTo != "" && hr.UpgradeTo != a.cfg.Version {
			a.mu.Lock()
			already := a.upgrading
			a.mu.Unlock()
			if !already {
				go a.selfUpgrade(hr.UpgradeTo)
			}
		}
	} else {
		log.Printf("heartbeat: status %d", resp.StatusCode)
	}
}

func (a *Agent) refreshPolicy(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.Server+"/api/agent/"+a.state.AgentID+"/policy", nil)
	if err != nil {
		log.Printf("policy: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+a.state.Secret)
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("policy: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("policy: status %d", resp.StatusCode)
		return
	}
	var p policy
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		log.Printf("policy decode: %v", err)
		return
	}
	a.mu.Lock()
	a.policy = p
	a.lastPolicyPull = time.Now()
	a.mu.Unlock()
}

func (a *Agent) upload(ctx context.Context) {
	a.mu.Lock()
	p := a.policy
	a.mu.Unlock()

	// Randomize size within [min,max] when a range is configured.
	sizeMB := p.SizeMB
	if p.SizeMaxMB > p.SizeMinMB {
		sizeMB = p.SizeMinMB + mrand.IntN(p.SizeMaxMB-p.SizeMinMB+1)
	}
	if sizeMB <= 0 {
		sizeMB = 1
	}

	// Data plane goes through the tunnel URL when provided; else --server.
	base := p.UploadURL
	if base == "" {
		base = a.cfg.Server
	}

	n := int64(sizeMB) * 1024 * 1024
	url := strings.TrimRight(base, "/") + "/upload/" + a.state.AgentID
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, io.LimitReader(rand.Reader, n))
	if err != nil {
		log.Printf("upload: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+a.state.Secret)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = n

	start := time.Now()
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("upload: %v", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusOK {
		a.mu.Lock()
		a.lastUpload = time.Now()
		a.mu.Unlock()
		log.Printf("uploaded %d bytes (%dMB) in %s", n, sizeMB, time.Since(start).Round(time.Millisecond))
	} else {
		log.Printf("upload: status %d", resp.StatusCode)
	}
}

// selfUpgrade downloads the agent binary for target version (via gh-proxy.org),
// sanity-checks its size, atomically replaces the running binary, and restarts
// the systemd service.
func (a *Agent) selfUpgrade(target string) {
	a.mu.Lock()
	if a.upgrading {
		a.mu.Unlock()
		return
	}
	a.upgrading = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.upgrading = false
		a.mu.Unlock()
	}()

	exe, err := os.Executable()
	if err != nil {
		log.Printf("self-upgrade: %v", err)
		return
	}
	arch := "amd64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	url := fmt.Sprintf("https://gh-proxy.org/https://github.com/%s/releases/download/%s/traffic-keeper-agent-linux-%s", upgradeRepo, target, arch)
	log.Printf("self-upgrade → %s: downloading %s", target, url)

	tmp := exe + ".new"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
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

	// Sanity check: a real agent binary is at least 1 MB.
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

	// Best effort: ask systemd to restart us. If that fails, exit so systemd's
	// Restart=always picks up the new binary.
	if err := exec.Command("systemctl", "restart", "traffic-keeper-agent").Start(); err != nil {
		log.Printf("self-upgrade: systemctl restart failed (%v); exiting for restart", err)
		os.Exit(0)
	}
}

func (a *Agent) loadState() error {
	b, err := os.ReadFile(a.cfg.State)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &a.state)
}

func (a *Agent) saveState() error {
	if dir := filepath.Dir(a.cfg.State); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(a.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.cfg.State, b, 0o600)
}
