package master

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

// cloudflaredBinary is where we install cloudflared.
const cloudflaredBinary = "/usr/local/bin/cloudflared"

// cloudflaredDownloadURL uses the gh-proxy.org mirror (CN networks can't pull
// github releases directly — same issue as the agent installer).
const cloudflaredDownloadURL = "https://gh-proxy.org/https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64"

const (
	tunnelTarget   = "http://localhost:8080" // master's own listener
	maxTunnelLogs  = 300
	maxEdgeHistory = 50
)

var trycloudflareRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// TunnelManager installs/runs a cloudflared quick tunnel pointing at the master,
// exposes the assigned trycloudflare URL, and keeps a rolling log buffer that
// the panel polls to show install/connection progress. It also runs edge-IP
// optimization (probe CF IPs, inject the best one via cloudflared --edge).
type TunnelManager struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	enabled bool
	url     string
	logs    []string

	// edge-IP optimization
	edgeMode       string // "off" | "auto" | "manual"
	configuredEdge string // edge IP injected via --edge ("" = none)
	probeResults   []EdgeResult
	probeRunning   bool
	probeAt        time.Time
	edgeHistory    []EdgeSwitch
	protocolMode   string // "http2" | "quic" — cloudflared --protocol
}

func newTunnelManager() *TunnelManager {
	// Default to http2 (TCP 443): verified ~20x faster than QUIC under current
	// GFW interference on UDP 7874. See Enable().
	return &TunnelManager{protocolMode: "http2"}
}

// Status returns a snapshot for the panel.
func (t *TunnelManager) Status() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	logs := make([]string, len(t.logs))
	copy(logs, t.logs)
	mode := t.edgeMode
	if mode == "" {
		mode = "off"
	}
	proto := t.protocolMode
	if proto == "" {
		proto = "http2"
	}
	currentEdge := detectCurrentEdgeIP(logs)
	edgeSource := "log"
	if currentEdge == "" {
		currentEdge = t.configuredEdge
		edgeSource = "config"
	}
	curLatency := 0
	for _, r := range t.probeResults {
		if r.IP == currentEdge {
			curLatency = r.LatencyMs
			break
		}
	}
	return map[string]any{
		"enabled":             t.enabled,
		"url":                 t.url,
		"installed":           t.isInstalled(),
		"logs":                logs,
		"edge_mode":           mode,
		"protocol":            proto,
		"configured_edge":     t.configuredEdge,
		"current_edge":        currentEdge,
		"current_edge_source": edgeSource,
		"current_latency_ms":  curLatency,
		"probe_running":       t.probeRunning,
		"probe_at":            t.probeAt.Unix(),
		"probe_results":       t.probeResults,
		"edge_history":        t.edgeHistory,
	}
}

// UploadURL returns the tunnel base URL if the tunnel is up, else "" (agents
// then fall back to their configured --server).
func (t *TunnelManager) UploadURL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url
}

// Enabled reports whether a tunnel is intended to be up. This is distinct from
// UploadURL (which is "" while cloudflared is still negotiating a quick-tunnel
// URL after a master restart): agents use it to back off instead of falling
// back to a direct connection that GFW would RST during that window.
func (t *TunnelManager) Enabled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.enabled
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
// asynchronously from cloudflared's stderr. If configuredEdge is set, cloudflared
// is pinned to that CF edge IP via --edge <ip>:7844 (edge optimization).
func (t *TunnelManager) Enable(ctx context.Context) error {
	t.mu.Lock()
	if t.enabled {
		t.mu.Unlock()
		return fmt.Errorf("tunnel already enabled")
	}
	edge := t.configuredEdge
	proto := t.protocolMode
	t.mu.Unlock()
	if proto == "" {
		proto = "http2"
	}

	if err := t.Install(ctx); err != nil {
		return err
	}

	t.appendLog("启动 quick tunnel（指向 " + tunnelTarget + "，协议 " + proto + "）...")
	// http2 (TCP 443) by default — verified ~20x faster than QUIC under the
	// current GFW interference on UDP 7874 (QUIC degrades to ~55KB/s & 524s;
	// http2 does ~1.1MB/s). cloudflared otherwise auto-degrades conservatively;
	// pinning the protocol avoids that.
	args := []string{"tunnel", "--url", tunnelTarget, "--no-autoupdate", "--protocol", proto}
	// --edge (优选 IP) only helps QUIC; http2 on TCP 443 is already fast and
	// doesn't use the QUIC edge port, so don't pin an edge under http2.
	if proto == "quic" && edge != "" {
		args = append(args, "--edge", edge+":7844")
		t.appendLog("优选 edge IP: " + edge + " (--edge " + edge + ":7844)")
	}
	cmd := exec.Command(cloudflaredBinary, args...)
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

// --- edge-IP optimization ---

// SetEdgeMode sets the optimization mode ("off"|"auto"|"manual").
func (t *TunnelManager) SetEdgeMode(mode string) {
	t.mu.Lock()
	t.edgeMode = mode
	t.mu.Unlock()
}

// SetConfiguredEdge sets the edge IP to inject on next enable.
func (t *TunnelManager) SetConfiguredEdge(ip string) {
	t.mu.Lock()
	t.configuredEdge = ip
	t.mu.Unlock()
}

// ApplyEdge records the edge IP and, if the tunnel is up, restarts cloudflared
// to pin it via --edge. Records a history entry.
func (t *TunnelManager) ApplyEdge(ctx context.Context, ip string) {
	t.mu.Lock()
	from := t.configuredEdge
	t.configuredEdge = ip
	wasEnabled := t.enabled
	t.mu.Unlock()
	if wasEnabled {
		t.appendLog("应用优选 edge IP " + ip + "，重启 cloudflared...")
		_ = t.Disable()
		_ = t.Enable(ctx)
	} else {
		t.appendLog("优选 edge IP 已设置（tunnel 未启用，下次启用生效）: " + ip)
	}
	t.recordEdgeSwitch(from, ip, "manual")
}

func (t *TunnelManager) recordEdgeSwitch(from, to, why string) {
	t.mu.Lock()
	t.edgeHistory = append(t.edgeHistory, EdgeSwitch{At: time.Now().Unix(), From: from, To: to, Why: why})
	if len(t.edgeHistory) > maxEdgeHistory {
		t.edgeHistory = t.edgeHistory[len(t.edgeHistory)-maxEdgeHistory:]
	}
	t.mu.Unlock()
}

// RunProbeAsync fetches the CF CIDR list, builds candidate IPs, probes them,
// and stores the sorted results. Best-effort, runs in the background; the panel
// polls Status() (probe_running/probe_results) for progress.
func (t *TunnelManager) RunProbeAsync(ctx context.Context, st *store.Store) {
	t.mu.Lock()
	if t.probeRunning {
		t.mu.Unlock()
		return
	}
	t.probeRunning = true
	t.mu.Unlock()

	go func() {
		defer func() {
			t.mu.Lock()
			t.probeRunning = false
			t.mu.Unlock()
		}()
		cidrList, _ := st.GetSetting(ctx, settingCFCIDRs)
		if fresh := fetchCFCIDRs(ctx); fresh != "" {
			cidrList = fresh
			_ = st.SetSetting(ctx, settingCFCIDRs, fresh)
		}
		ips := cfCandidateIPs(cidrList)
		t.appendLog(fmt.Sprintf("edge 测速开始: %d 个候选 IP", len(ips)))
		results := probeEdgeIPs(ctx, ips, 3, 20)
		t.mu.Lock()
		t.probeResults = results
		t.probeAt = time.Now()
		t.mu.Unlock()
		top, lat, loss := "—", 0, 0.0
		if len(results) > 0 && results[0].LossPct < 100 {
			top, lat, loss = results[0].IP, results[0].LatencyMs, results[0].LossPct
		}
		t.appendLog(fmt.Sprintf("edge 测速完成: top %s (%dms, 丢包 %.0f%%)", top, lat, loss))
	}()
}

// --- handlers ---

// handleTunnel: GET = status, POST = (re)enable (async).
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.tunnel.Status())
	case http.MethodPost:
		_ = s.store.SetSetting(r.Context(), settingTunnelEnabled, "1") // persist intent (not URL)
		if edgeIP, err := s.store.GetSetting(r.Context(), settingTunnelEdgeIP); err == nil {
			s.tunnel.SetConfiguredEdge(edgeIP) // carry the configured edge IP into this enable
		}
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

// handleEdgeTest: POST → trigger an async edge-IP probe.
func (s *Server) handleEdgeTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.tunnel.RunProbeAsync(context.Background(), s.store) // not r.Context(): probe outlives the request
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleEdgeApply: POST {ip, mode} → apply a manual edge IP and/or set mode.
func (s *Server) handleEdgeApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		IP   string `json:"ip"`
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.Mode != "" {
		switch body.Mode {
		case "off":
			_ = s.store.SetSetting(r.Context(), settingTunnelEdgeMode, "off")
			s.tunnel.SetEdgeMode("off")
			s.tunnel.ApplyEdge(r.Context(), "") // clear injected edge, restart cloudflared if up
			_ = s.store.SetSetting(r.Context(), settingTunnelEdgeIP, "")
		case "auto", "manual":
			_ = s.store.SetSetting(r.Context(), settingTunnelEdgeMode, body.Mode)
			s.tunnel.SetEdgeMode(body.Mode)
		default:
			http.Error(w, "invalid mode", http.StatusBadRequest)
			return
		}
	}
	if body.IP != "" {
		parsed := net.ParseIP(body.IP)
		if parsed == nil || parsed.To4() == nil {
			http.Error(w, "invalid ip", http.StatusBadRequest)
			return
		}
		ip4 := parsed.To4().String()
		_ = s.store.SetSetting(r.Context(), settingTunnelEdgeIP, ip4)
		s.tunnel.ApplyEdge(r.Context(), ip4)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// RestoreTunnelIntent re-enables the tunnel on boot if the user last left it on.
// Call after the HTTP listener is up so cloudflared's upstream is reachable.
func (s *Server) RestoreTunnelIntent(ctx context.Context) {
	v, err := s.store.GetSetting(ctx, settingTunnelEnabled)
	if err != nil || v != "1" {
		return
	}
	if edgeIP, err := s.store.GetSetting(ctx, settingTunnelEdgeIP); err == nil {
		s.tunnel.SetConfiguredEdge(edgeIP)
	}
	if mode, err := s.store.GetSetting(ctx, settingTunnelEdgeMode); err == nil {
		s.tunnel.SetEdgeMode(mode)
	}
	log.Printf("tunnel: restoring (intent=enabled), launching cloudflared...")
	go s.tunnel.Enable(ctx)
}
