package master

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProbeEdgeIPs(t *testing.T) {
	orig := dialProbe
	defer func() { dialProbe = orig }()
	dialProbe = func(ctx context.Context, ip string, timeout time.Duration) (time.Duration, error) {
		switch ip {
		case "1.1.1.1":
			return 10 * time.Millisecond, nil
		case "2.2.2.2":
			return 100 * time.Millisecond, nil
		default:
			return 0, errors.New("unreachable")
		}
	}
	results := probeEdgeIPs(context.Background(), []string{"3.3.3.3", "2.2.2.2", "1.1.1.1"}, 3, 3)
	if len(results) != 3 {
		t.Fatalf("len=%d", len(results))
	}
	// sorted by loss asc then latency asc: 1.1.1.1 (0%,10ms) < 2.2.2.2 (0%,100ms) < 3.3.3.3 (100%)
	if results[0].IP != "1.1.1.1" || results[1].IP != "2.2.2.2" || results[2].IP != "3.3.3.3" {
		t.Fatalf("order wrong: %+v", results)
	}
	if results[0].LossPct != 0 || results[0].LatencyMs != 10 {
		t.Fatalf("top result: %+v", results[0])
	}
	if results[2].LossPct != 100 || results[2].Samples != 0 {
		t.Fatalf("dead IP should be 100%% loss: %+v", results[2])
	}
}

func TestDetectCurrentEdgeIP(t *testing.T) {
	logs := []string{
		"starting cloudflared",
		"Registered tunnel connection edge:[104.16.0.1:7844]",
		"Registered tunnel connection edge:[162.159.0.1:7844]",
	}
	if got := detectCurrentEdgeIP(logs); got != "162.159.0.1" {
		t.Fatalf("expected most recent 162.159.0.1, got %q", got)
	}
	if got := detectCurrentEdgeIP([]string{"no edge here"}); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestSampleCIDRAvoidsNetworkAddr(t *testing.T) {
	// big CIDR: step is a multiple of 256, so naive step*k lands on .0
	ips := sampleCIDR("104.16.0.0/20", 4)
	if len(ips) == 0 {
		t.Fatal("no samples")
	}
	for _, ip := range ips {
		parts := strings.Split(ip, ".")
		if len(parts) != 4 {
			t.Fatalf("bad ip %q", ip)
		}
		if parts[3] == "0" {
			t.Fatalf("sampled network address .0 (often unreachable): %s", ip)
		}
		if !strings.HasPrefix(ip, "104.16.") {
			t.Fatalf("ip %q out of cidr", ip)
		}
	}
}
