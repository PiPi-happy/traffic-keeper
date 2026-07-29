package master

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"sync"
)

// cloudflaredBinary is where we install cloudflared.
const cloudflaredBinary = "/usr/local/bin/cloudflared"

// cloudflaredDownloadURL uses the gh-proxy.org mirror (CN networks can't pull
// github releases directly — same issue as the agent installer).
const cloudflaredDownloadURL = "https://gh-proxy.org/https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64"

const (
	tunnelTarget  = "http://localhost:8080" // master's own listener
	maxTunnelLogs = 300
)

var trycloudflareRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// TunnelManager installs/runs a cloudflared quick tunnel pointing at the master,
// exposes the assigned trycloudflare URL, and keeps a rolling log buffer that
// the panel polls to show install/connection progress.
type TunnelManager struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	enabled bool
	url     string
	logs    []string
}

func newTunnelManager() *TunnelManager {
	return &TunnelManager{}
}

// Status returns a snapshot for the panel.
func (t *TunnelManager) Status() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	logs := make([]string, len(t.logs))
	copy(logs, t.logs)
	return map[string]any{
		"enabled":   t.enabled,
		"url":       t.url,
		"installed": t.isInstalled(),
		"logs":      logs,
	}
}

// UploadURL returns the tunnel base URL if the tunnel is up, else "" (agents
// then fall back to their configured --server).
func (t *TunnelManager) UploadURL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url
}

func (t *TunnelManager) isInstalled() bool {
	_, err := exec.LookPath("cloudflared")
	if err != nil {
		// also accept the fixed path
		if _, err := exec.Command(cloudflaredBinary, "--version").CombinedOutput(); err == nil {
			return true
		}
		return false
	}
	return true
}

func (t *TunnelManager) appendLog(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.logs = append(t.logs, line)
	if len(t.logs) > maxTunnelLogs {
		t.logs = t.logs[len(t.logs)-maxTunnelLogs:]
	}
}

// ResetLogs clears the log buffer (called when (re)enabling from the panel).
func (t *TunnelManager) ResetLogs() {
	t.mu.Lock()
	t.logs = nil
	t.mu.Unlock()
}

// Install downloads cloudflared if missing. Safe to call when already installed.
func (t *TunnelManager) Install(ctx context.Context) error {
	if t.isInstalled() {
		return nil
	}
	t.appendLog("正在下载 cloudflared（经 gh-proxy.org）...")
	cmd := exec.CommandContext(ctx, "bash", "-c",
		fmt.Sprintf(`curl -fsSL -o %s %s && chmod +x %s`, cloudflaredBinary, cloudflaredDownloadURL, cloudflaredBinary))
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.appendLog("下载失败: " + err.Error())
		if out.Len() > 0 {
			t.appendLog(out.String())
		}
		return err
	}
	t.appendLog("cloudflared 安装完成")
	return nil
}

// Enable installs (if needed) and starts the quick tunnel. It returns once the
// cloudflared process is launched; the trycloudflare URL is captured
// asynchronously from cloudflared's stderr.
func (t *TunnelManager) Enable(ctx context.Context) error {
	t.mu.Lock()
	if t.enabled {
		t.mu.Unlock()
		return fmt.Errorf("tunnel already enabled")
	}
	t.mu.Unlock()

	if err := t.Install(ctx); err != nil {
		return err
	}

	t.appendLog("启动 quick tunnel（指向 " + tunnelTarget + "）...")
	cmd := exec.Command(cloudflaredBinary, "tunnel", "--url", tunnelTarget, "--no-autoupdate")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.appendLog("启动失败: " + err.Error())
		return err
	}
	if err := cmd.Start(); err != nil {
		t.appendLog("启动失败: " + err.Error())
		return err
	}
	t.mu.Lock()
	t.cmd = cmd
	t.enabled = true
	t.mu.Unlock()
	t.appendLog("cloudflared 进程已启动，等待分配 trycloudflare 地址...")

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			t.appendLog(line)
			if m := trycloudflareRe.FindString(line); m != "" {
				t.mu.Lock()
				if t.url == "" {
					t.url = m
				}
				t.mu.Unlock()
			}
		}
		// scanner ended = cloudflared exited
		t.mu.Lock()
		t.enabled = false
		t.cmd = nil
		t.url = ""
		t.mu.Unlock()
		t.appendLog("cloudflared 进程已退出")
	}()

	return nil
}

// Disable stops the running tunnel.
func (t *TunnelManager) Disable() error {
	t.mu.Lock()
	cmd := t.cmd
	t.cmd = nil
	t.enabled = false
	t.url = ""
	t.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	t.appendLog("tunnel 已关闭")
	return nil
}

// handleTunnel: GET = status, POST = (re)enable (async).
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.tunnel.Status())
	case http.MethodPost:
		_ = s.store.SetSetting(r.Context(), settingTunnelEnabled, "1") // persist intent (not URL)
		s.tunnel.ResetLogs()
		go s.tunnel.Enable(context.Background())
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleTunnelDisable: POST = stop the tunnel.
func (s *Server) handleTunnelDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.tunnel.Disable()
	_ = s.store.SetSetting(r.Context(), settingTunnelEnabled, "0")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// RestoreTunnelIntent re-enables the tunnel on boot if the user last left it on.
// Call after the HTTP listener is up so cloudflared's upstream is reachable.
func (s *Server) RestoreTunnelIntent(ctx context.Context) {
	v, err := s.store.GetSetting(ctx, settingTunnelEnabled)
	if err != nil || v != "1" {
		return
	}
	log.Printf("tunnel: restoring (intent=enabled), launching cloudflared...")
	go s.tunnel.Enable(ctx)
}
