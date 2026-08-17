package master

import (
	"context"
	"log"
	"time"

	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

// Upload-health sentinel. A healthy agent heartbeats (control plane, direct)
// AND uploads (data plane, via tunnel). The two can diverge silently — in the
// 2026-08-14 prod incident the quick tunnel was deregistered by Cloudflare, so
// agents kept heartbeating "online" while every upload died at the CF edge and
// nothing reached the master for 3 days. This sentinel surfaces that gap:
// per-node in the panel (upload_stale flag) and via journal ERROR logs.

const (
	uploadStaleMinThreshold = 45 * 60 // seconds; floor so fast schedules don't flake
	uploadWatchInterval     = 5 * time.Minute
)

// uploadStaleThreshold returns how long an agent may go without a successful
// upload before being flagged: 3× its longest possible interval plus 5 minutes
// of slack, floored at 45 minutes (covers upload backoff/retry windows).
func uploadStaleThreshold(p store.Policy) int64 {
	longest := p.IntervalSec
	if p.IntervalMaxSec > longest {
		longest = p.IntervalMaxSec
	}
	if longest < 1 {
		longest = 1
	}
	th := int64(longest)*3 + 300
	if th < uploadStaleMinThreshold {
		th = uploadStaleMinThreshold
	}
	return th
}

// agentUploadStale reports whether the agent is expected to upload (node +
// policy enabled) and heartbeating, but hasn't completed an upload in longer
// than its threshold. Also returns seconds since the last upload (or since
// node creation if it never uploaded).
func agentUploadStale(a store.Agent, p store.Policy, st store.Stats, now int64) (bool, int64) {
	if !a.Enabled || !p.Enabled || !isOnline(a.LastSeenAt) {
		return false, 0
	}
	last := st.LastUploadAt
	if last == 0 {
		last = a.CreatedAt // never uploaded: measure from creation
	}
	if last == 0 {
		return false, 0
	}
	since := now - last
	if since < 0 {
		since = 0
	}
	return since > uploadStaleThreshold(p), since
}

// watchUploadHealth scans all agents every few minutes and logs an ERROR for
// each online-but-not-uploading one, so prolonged data-plane outages leave a
// visible trail in the journal even if nobody has the panel open.
func (s *Server) watchUploadHealth() {
	ticker := time.NewTicker(uploadWatchInterval)
	defer ticker.Stop()
	for range ticker.C {
		ctx := context.Background()
		agents, err := s.store.ListAgents(ctx)
		if err != nil {
			continue
		}
		stats, _ := s.store.AllStats(ctx)
		now := time.Now().Unix()
		for _, a := range agents {
			p, err := s.store.GetPolicy(ctx, a.ID)
			if err != nil {
				continue
			}
			if stale, since := agentUploadStale(a, p, stats[a.ID], now); stale {
				log.Printf("upload sentinel: agent %s (%s) is online but no successful upload for %dmin", a.ID, a.Name, since/60)
			}
		}
	}
}
