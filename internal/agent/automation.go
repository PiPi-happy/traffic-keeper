package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ifaceStats struct {
	RX int64
	TX int64
}

func readInterfaceStats(name string) (ifaceStats, error) {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return ifaceStats{}, err
	}
	var total ifaceStats
	found := false
	for _, entry := range entries {
		iface := entry.Name()
		if iface == "lo" || (name != "" && iface != name) {
			continue
		}
		base := filepath.Join("/sys/class/net", iface, "statistics")
		rx, e1 := os.ReadFile(filepath.Join(base, "rx_bytes"))
		tx, e2 := os.ReadFile(filepath.Join(base, "tx_bytes"))
		if e1 != nil || e2 != nil {
			continue
		}
		var r, t int64
		if _, err := fmt.Sscan(strings.TrimSpace(string(rx)), &r); err != nil {
			continue
		}
		if _, err := fmt.Sscan(strings.TrimSpace(string(tx)), &t); err != nil {
			continue
		}
		total.RX += r
		total.TX += t
		found = true
	}
	if !found {
		return total, fmt.Errorf("no readable network interface statistics")
	}
	return total, nil
}

func (a *Agent) automationLoop(ctx context.Context) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.automationTick(ctx)
		}
	}
}

func (a *Agent) activeAutomation() automationPolicy {
	a.runMu.Lock()
	conns := make([]*masterConn, 0, len(a.masters))
	for _, c := range a.masters {
		conns = append(conns, c)
	}
	a.runMu.Unlock()
	for _, c := range conns {
		c.mu.Lock()
		p := c.automation
		c.mu.Unlock()
		if p.Enabled {
			return p
		}
	}
	return automationPolicy{}
}

func (a *Agent) automationTick(ctx context.Context) {
	p := a.activeAutomation()
	if !p.Enabled {
		return
	}
	a.automationMu.Lock()
	reset := a.automation.Interface != p.Interface || a.automation.WindowHours != p.WindowHours
	a.automation = p
	a.automationMu.Unlock()
	if reset {
		a.resetSamplingBaseline()
	}
	a.automationMu.Lock()
	if a.automationRunning {
		a.automationMu.Unlock()
		return
	}
	now := time.Now()
	next := time.Duration(p.CheckMinSec) * time.Second
	if p.CheckMaxSec > p.CheckMinSec {
		next = time.Duration(p.CheckMinSec+rand.IntN(p.CheckMaxSec-p.CheckMinSec+1)) * time.Second
	}
	if !a.lastAutomationCheck.IsZero() && now.Sub(a.lastAutomationCheck) < next {
		a.automationMu.Unlock()
		return
	}
	a.lastAutomationCheck = now
	a.automationMu.Unlock()
	stats, err := readInterfaceStats(p.Interface)
	if err != nil {
		log.Printf("automation: interface stats: %v", err)
		return
	}
	samples := a.appendSample(stats)
	if len(p.Sources) == 0 {
		return
	}
	cutoff := now.Add(-time.Duration(maxInt(p.WindowHours, 1)) * time.Hour).Unix()
	var old *trafficSample
	for i := range samples {
		if samples[i].At <= cutoff && (old == nil || samples[i].At > old.At) {
			old = &samples[i]
		}
	}
	if old == nil {
		return // no complete configured window baseline; don't use a partial ratio
	}
	down := stats.RX - old.RX
	up := stats.TX - old.TX
	if down < 0 || up < 0 {
		return
	}
	target := float64(up) * p.TargetMultiplier
	if float64(down) >= target {
		return
	}
	if p.MaxTaskMB <= 0 {
		return
	}
	ordered := append([]automationSource(nil), p.Sources...)
	rand.Shuffle(len(ordered), func(i, j int) { ordered[i], ordered[j] = ordered[j], ordered[i] })
	a.automationMu.Lock()
	a.automationRunning = true
	a.automationMu.Unlock()
	go func() {
		defer func() { a.automationMu.Lock(); a.automationRunning = false; a.automationMu.Unlock() }()
		for _, src := range ordered {
			if ctx.Err() != nil {
				return
			}
			if err := downloadSource(ctx, a, src, p.MaxTaskMB); err != nil {
				log.Printf("automation download %s: %v", src.Name, err)
				_ = a.reportAutomation(ctx, src, 0, "fail", err.Error(), 0)
				continue
			}
			break // one APK per check; max_task_mb is a per-task ceiling
		}
	}()
}

func (a *Agent) appendSample(s ifaceStats) []trafficSample {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	now := time.Now().Unix()
	a.file.Samples = append(a.file.Samples, trafficSample{At: now, RX: s.RX, TX: s.TX})
	cut := now - 8*24*3600
	filtered := a.file.Samples[:0]
	for _, x := range a.file.Samples {
		if x.At >= cut {
			filtered = append(filtered, x)
		}
	}
	a.file.Samples = filtered
	if a.cfg.State != "" {
		if err := writeFile(a.cfg.State, a.file); err != nil {
			log.Printf("automation sample persistence: %v", err)
		}
	}
	return append([]trafficSample(nil), filtered...)
}

func (a *Agent) resetSamplingBaseline() {
	a.stateMu.Lock()
	a.file.Samples = nil
	if a.cfg.State != "" {
		if err := writeFile(a.cfg.State, a.file); err != nil {
			log.Printf("automation reset sample baseline: %v", err)
		}
	}
	a.stateMu.Unlock()
}

func downloadSource(ctx context.Context, a *Agent, src automationSource, taskMaxMB int) error {
	u, err := url.Parse(src.URL)
	if err != nil || u.Scheme != "https" {
		return fmt.Errorf("source must use HTTPS")
	}
	allowed := map[string]bool{}
	for _, h := range strings.Split(src.AllowedHosts, ",") {
		allowed[strings.ToLower(strings.TrimSpace(h))] = true
	}
	if !allowed[strings.ToLower(u.Hostname())] {
		return fmt.Errorf("source host is not allowed")
	}
	if strings.TrimSpace(src.AllowedHosts) == "" {
		return fmt.Errorf("source allowlist is empty")
	}
	if err := validatePublicHost(u.Hostname()); err != nil {
		return err
	}
	maxBytes := int64(taskMaxMB) * 1024 * 1024
	if src.MaxMB > 0 && int64(src.MaxMB)*1024*1024 < maxBytes {
		maxBytes = int64(src.MaxMB) * 1024 * 1024
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if req.URL.Scheme != "https" || !allowed[strings.ToLower(req.URL.Hostname())] {
			return fmt.Errorf("redirect host is not allowed")
		}
		if err := validatePublicHost(req.URL.Hostname()); err != nil {
			return err
		}
		return nil
	}}
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(ct, "apk") && !strings.Contains(ct, "octet-stream") && !strings.Contains(ct, "binary") && !strings.HasSuffix(strings.ToLower(resp.Request.URL.Path), ".apk") {
		return fmt.Errorf("unexpected content type %q", ct)
	}
	tmp, err := os.CreateTemp("", "traffic-keeper-*.apk")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	n, copyErr := io.Copy(tmp, io.LimitReader(resp.Body, maxBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxBytes {
		return fmt.Errorf("download exceeded task size limit")
	}
	if n == 0 {
		return fmt.Errorf("empty download")
	}
	if n < 4 {
		return fmt.Errorf("download is not a valid APK")
	}
	f, err := os.Open(tmpName)
	if err != nil {
		return err
	}
	magic := make([]byte, 4)
	_, readErr := io.ReadFull(f, magic)
	_ = f.Close()
	if readErr != nil || string(magic) != "PK\x03\x04" {
		return fmt.Errorf("download is not a valid APK archive")
	}
	return a.reportAutomation(ctx, src, n, "ok", "", time.Since(start))
}

func validatePublicHost(host string) error {
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("cannot resolve source host")
	}
	for _, ip := range ips {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || !ip.IsGlobalUnicast() {
			return fmt.Errorf("source host resolves to a non-public address")
		}
	}
	return nil
}

func (a *Agent) reportAutomation(ctx context.Context, src automationSource, n int64, status, msg string, d time.Duration) error {
	a.runMu.Lock()
	conns := make([]*masterConn, 0, len(a.masters))
	for _, c := range a.masters {
		conns = append(conns, c)
	}
	a.runMu.Unlock()
	for _, c := range conns {
		c.mu.Lock()
		id, secret := c.agentID, c.secret
		c.mu.Unlock()
		payload, _ := json.Marshal(map[string]any{"agent_id": id, "source_id": src.ID, "app_name": src.Name, "started_at": time.Now().Unix() - int64(d.Seconds()), "bytes": n, "status": status, "error": msg, "duration_ms": d.Milliseconds()})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.server+"/api/agent/automation/event", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.client.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
