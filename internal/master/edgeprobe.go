package master

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// EdgeResult is one CF edge IP's measured quality.
type EdgeResult struct {
	IP        string  `json:"ip"`
	LatencyMs int     `json:"latency_ms"` // median TCP:443 handshake RTT (0 if unreachable)
	JitterMs  int     `json:"jitter_ms"`  // stdev of samples
	LossPct   float64 `json:"loss_pct"`   // failed dials / total dials (%)
	Samples   int     `json:"samples"`    // successful dials
}

// EdgeSwitch is one edge-IP change event (manual or automatic), for history.
type EdgeSwitch struct {
	At   int64  `json:"at"` // unix seconds
	From string `json:"from"`
	To   string `json:"to"`
	Why  string `json:"why"` // "manual" | "auto" | ...
}

// edgeAddrRe matches cloudflared's stderr "edge:[<ip>:7844]" line printed when
// it registers a tunnel connection.
var edgeAddrRe = regexp.MustCompile(`edge:\[([0-9.]+):7844\]`)

// dialProbe dials one IP:443 once and returns the TCP handshake duration.
// Overridable in tests.
var dialProbe = func(ctx context.Context, ip string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, "443"))
	dt := time.Since(start)
	if err != nil {
		return dt, err
	}
	_ = conn.Close()
	return dt, nil
}

// probeEdgeIPs dials each IP's TCP:443 `samples` times concurrently (bounded by
// `concurrency`) and returns EdgeResults sorted by loss asc then latency asc.
// CF is anycast, so the PoP reached on 443 is the one UDP 7844 (cloudflared
// QUIC) also uses — 443 latency/loss is a proxy for the QUIC path quality.
func probeEdgeIPs(ctx context.Context, ips []string, samples, concurrency int) []EdgeResult {
	if samples <= 0 {
		samples = 3
	}
	if concurrency <= 0 {
		concurrency = 20
	}
	results := make([]EdgeResult, len(ips))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, ip := range ips {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, ip string) {
			defer wg.Done()
			defer func() { <-sem }()
			r := EdgeResult{IP: ip}
			var lats []float64 // ms
			for s := 0; s < samples; s++ {
				d, err := dialProbe(ctx, ip, 1500*time.Millisecond)
				if err != nil {
					continue
				}
				lats = append(lats, float64(d.Microseconds())/1000.0)
			}
			r.Samples = len(lats)
			r.LossPct = float64(samples-len(lats)) / float64(samples) * 100
			if len(lats) > 0 {
				sort.Float64s(lats)
				r.LatencyMs = int(median(lats))
				r.JitterMs = int(stdev(lats))
			}
			results[idx] = r
		}(i, ip)
	}
	wg.Wait()
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].LossPct != results[j].LossPct {
			return results[i].LossPct < results[j].LossPct
		}
		return results[i].LatencyMs < results[j].LatencyMs
	})
	return results
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func stdev(xs []float64) float64 {
	n := float64(len(xs))
	if n == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / n
	var sq float64
	for _, x := range xs {
		sq += (x - mean) * (x - mean)
	}
	return math.Sqrt(sq / n)
}

// cfFallbackCIDRs is used if fetching the live list fails.
var cfFallbackCIDRs = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
}

// cfCandidateIPs returns a deduped list of CF edge IPs to probe: `cidrList`
// (the cached live ips-v4, fallback to cfFallbackCIDRs) sampled a few per CIDR,
// plus the IPs the QUIC region hostnames currently resolve to.
func cfCandidateIPs(cidrList string) []string {
	cidrs := parseCIDRList(cidrList)
	if len(cidrs) == 0 {
		cidrs = cfFallbackCIDRs
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(ip string) {
		if ip == "" {
			return
		}
		if _, ok := seen[ip]; ok {
			return
		}
		seen[ip] = struct{}{}
		out = append(out, ip)
	}
	for _, c := range cidrs {
		for _, ip := range sampleCIDR(c, 4) {
			add(ip)
		}
	}
	for _, host := range []string{"region1.v2.quic.cdn-cloudflare.com", "region2.v2.quic.cdn-cloudflare.com"} {
		if ips, err := net.LookupIP(host); err == nil {
			for _, ip := range ips {
				if v4 := ip.To4(); v4 != nil {
					add(v4.String())
				}
			}
		}
	}
	return out
}

func parseCIDRList(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.Contains(line, "/") && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

// sampleCIDR returns up to n host IPs evenly picked from an IPv4 CIDR.
func sampleCIDR(cidr string, n int) []string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil
	}
	ip := ipnet.IP.To4()
	if ip == nil || len(ipnet.Mask) != 4 {
		return nil
	}
	ones, bits := ipnet.Mask.Size()
	hostBits := bits - ones
	total := uint64(1) << hostBits
	base := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	step := uint64(1)
	if n > 0 && uint64(n) < total {
		step = total / uint64(n)
	}
	var out []string
	for i := step; i < total && len(out) < n; i += step {
		x := base + uint32(i)
		out = append(out, fmt.Sprintf("%d.%d.%d.%d", x>>24&0xff, x>>16&0xff, x>>8&0xff, x&0xff))
	}
	return out
}

// fetchCFCIDRs downloads cloudflare.com/ips-v4 (best effort; "" on failure).
func fetchCFCIDRs(ctx context.Context) string {
	const url = "https://www.cloudflare.com/ips-v4"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(b)
}

// detectCurrentEdgeIP scrapes cloudflared's stderr log lines (captured by
// TunnelManager.appendLog) for the most recent "edge:[<ip>:7844]" it printed
// when registering a tunnel connection. More reliable than ss, which often
// can't see cloudflared's UDP socket state.
func detectCurrentEdgeIP(logs []string) string {
	for i := len(logs) - 1; i >= 0; i-- {
		if m := edgeAddrRe.FindStringSubmatch(logs[i]); len(m) > 1 {
			return m[1]
		}
	}
	return ""
}
