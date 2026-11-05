package master

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

func (s *Server) handleAutomation(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		c, err := s.store.GetAutomationConfig(r.Context())
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, c)
	case http.MethodPut:
		var c store.AutomationConfig
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&c) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if c.TargetMultiplier < 1 || c.TargetMultiplier > 100 || c.WindowHours < 1 || c.WindowHours > 168 || c.CheckMinSec < 60 || c.CheckMaxSec < c.CheckMinSec || c.MaxTaskMB < 1 || c.MaxTaskMB > 2048 {
			http.Error(w, "invalid automation settings", 400)
			return
		}
		sources, _ := s.store.ListAppSources(r.Context())
		enabled := map[string]bool{}
		for _, x := range sources {
			if x.Enabled {
				enabled[x.ID] = true
			}
		}
		for _, id := range c.Sources {
			if !enabled[id] {
				http.Error(w, "source is not enabled", 400)
				return
			}
		}
		if c.Enabled && len(c.Sources) == 0 {
			http.Error(w, "select at least one enabled source", 400)
			return
		}
		if err := s.store.SetAutomationConfig(r.Context(), c); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, c)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleAutomationSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a, err := s.store.ListAppSources(r.Context())
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"sources": a})
	case http.MethodPost:
		var a store.AppSource
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&a) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		a.ID = strings.TrimSpace(a.ID)
		a.Name = strings.TrimSpace(a.Name)
		a.URL = strings.TrimSpace(a.URL)
		u, err := url.Parse(a.URL)
		if a.ID == "" || a.Name == "" || err != nil || u.Scheme != "https" || u.Host == "" || a.MaxMB < 1 || a.MaxMB > 2048 {
			http.Error(w, "source requires id, name, HTTPS URL and valid max_mb", 400)
			return
		}
		if a.AllowedHosts == "" {
			a.AllowedHosts = u.Hostname()
		}
		if a.Enabled {
			prev, _ := s.store.ListAppSources(r.Context())
			verified := false
			for _, old := range prev {
				if old.ID == a.ID && old.Verified && old.URL == a.URL && old.AllowedHosts == a.AllowedHosts {
					verified = true
					break
				}
			}
			if !verified {
				http.Error(w, "请先测试并验证该来源的 APK 直链，再启用", 400)
				return
			}
		}
		if strings.TrimSpace(a.AllowedHosts) == "" {
			http.Error(w, "allowed_hosts is required", 400)
			return
		}
		if err := s.store.SaveAppSource(r.Context(), a); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, a)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleAutomationSource(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/automation/sources/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", 405)
		return
	}
	if err := s.store.DeleteAppSource(r.Context(), id); err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAutomationSourceTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var body struct {
		ID           string `json:"id"`
		URL          string `json:"url"`
		AllowedHosts string `json:"allowed_hosts"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	u, err := url.Parse(body.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		http.Error(w, "HTTPS URL required", 400)
		return
	}
	allowed := map[string]bool{}
	for _, h := range strings.Split(body.AllowedHosts, ",") {
		allowed[strings.ToLower(strings.TrimSpace(h))] = true
	}
	if len(allowed) == 0 {
		allowed[strings.ToLower(u.Hostname())] = true
	}
	if err := ensurePublicHostname(u.Hostname()); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	client := http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, DialContext: publicSourceDial}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 || req.URL.Scheme != "https" || req.URL.User != nil || !allowed[strings.ToLower(req.URL.Hostname())] {
			return http.ErrUseLastResponse
		}
		return ensurePublicHostname(req.URL.Hostname())
	}}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, body.URL, nil)
	if err != nil {
		http.Error(w, "bad URL", 400)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "source probe failed: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	magic := make([]byte, 4)
	limited := io.LimitReader(resp.Body, 4)
	n, _ := io.ReadFull(limited, magic)
	valid := resp.StatusCode == 200 && n == 4 && string(magic) == "PK\x03\x04"
	if valid && body.ID != "" {
		sources, _ := s.store.ListAppSources(r.Context())
		for _, src := range sources {
			if src.ID == body.ID && src.URL == body.URL && src.AllowedHosts == body.AllowedHosts {
				_ = s.store.MarkSourceVerified(r.Context(), body.ID, body.URL)
				break
			}
		}
	}
	writeJSON(w, 200, map[string]any{"ok": valid, "status": resp.StatusCode, "content_type": resp.Header.Get("Content-Type"), "content_length": resp.ContentLength, "final_url": resp.Request.URL.String(), "apk_magic": valid, "signature_prefix": fmt.Sprintf("%x", magic[:n])})
}

func ensurePublicHostname(host string) error {
	if net.ParseIP(host) != nil {
		return fmt.Errorf("IP literal download sources are not allowed")
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("source hostname cannot be resolved")
	}
	for _, ip := range ips {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || !ip.IsGlobalUnicast() {
			return fmt.Errorf("source hostname must resolve to public IPs")
		}
	}
	return nil
}

func publicSourceDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("source hostname cannot be resolved")
	}
	var lastErr error
	for _, ip := range ips {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || !ip.IsGlobalUnicast() {
			return nil, fmt.Errorf("source hostname must resolve to public IPs")
		}
		conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
		lastErr = e
	}
	return nil, lastErr
}

func (s *Server) handleAutomationEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	events, err := s.store.ListAutomationEvents(r.Context(), time.Now().Add(-30*24*time.Hour).Unix(), 1000)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"events": events})
}

func (s *Server) handleAgentAutomationEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var e store.AutomationEvent
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&e) != nil || e.AgentID == "" || e.Bytes < 0 || e.DurationMs < 0 || len(e.Error) > 2048 || len(e.AppName) > 256 || len(e.SourceID) > 256 || e.StartedAt < time.Now().Add(-31*24*time.Hour).Unix() || e.StartedAt > time.Now().Add(5*time.Minute).Unix() {
		http.Error(w, "bad request", 400)
		return
	}
	if _, ok := s.authenticateAgent(r, e.AgentID); !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	if e.Status != "ok" && e.Status != "fail" && e.Status != "skipped" && e.Status != "cancelled" {
		http.Error(w, "invalid status", 400)
		return
	}
	if err := s.store.InsertAutomationEvent(r.Context(), e); err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
