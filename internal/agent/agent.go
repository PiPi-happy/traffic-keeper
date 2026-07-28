package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	heartbeatInterval  = 30 * time.Second
	policyPullInterval = 30 * time.Second
	tickInterval       = 10 * time.Second
	defaultTimeout     = 5 * time.Minute
)

// Config configures the agent.
type Config struct {
	Server string // master base URL, e.g. https://master.example.com
	Token  string // one-time install token (first-run registration only)
	State  string // path to the persisted credentials file
}

// policy mirrors the master policy the agent pulls.
type policy struct {
	Enabled     bool `json:"enabled"`
	IntervalSec int  `json:"interval_sec"`
	SizeMB      int  `json:"size_mb"`
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

	mu             sync.Mutex // guards policy + the *last** timestamps
	policy         policy
	lastHeartbeat  time.Time
	lastPolicyPull time.Time
	lastUpload     time.Time
}

// New creates an agent.
func New(cfg Config) *Agent {
	return &Agent{
		cfg:    cfg,
		client: &http.Client{Timeout: defaultTimeout},
		policy: policy{Enabled: true, IntervalSec: 1800, SizeMB: 50}, // defaults until first pull
	}
}

// Run registers (if needed) then runs heartbeat / policy / upload each in its
// own goroutine. A slow upload can never starve heartbeats, so the master
// won't mark the agent offline while a big upload is in flight.
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
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("heartbeat: %v", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusOK {
		a.mu.Lock()
		a.lastHeartbeat = time.Now()
		a.mu.Unlock()
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

	n := int64(p.SizeMB) * 1024 * 1024
	if n <= 0 {
		n = 1 << 20
	}
	// Stream incompressible random data straight from /dev/urandom; never hold
	// the whole payload in memory.
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, a.cfg.Server+"/upload/"+a.state.AgentID, io.LimitReader(rand.Reader, n))
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
		log.Printf("uploaded %d bytes in %s", n, time.Since(start).Round(time.Millisecond))
	} else {
		log.Printf("upload: status %d", resp.StatusCode)
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
