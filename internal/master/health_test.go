package master

import (
	"testing"
	"time"

	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

func TestUploadStaleThreshold(t *testing.T) {
	cases := []struct {
		p    store.Policy
		want int64
	}{
		{store.Policy{IntervalSec: 60}, 45 * 60},                                          // fast schedule → floor
		{store.Policy{IntervalSec: 60, IntervalMinSec: 30, IntervalMaxSec: 200}, 45 * 60}, // random range → floor
		{store.Policy{IntervalSec: 1800}, 3*1800 + 300},                                   // 30min interval → 95min
		{store.Policy{IntervalSec: 0, IntervalMaxSec: 7200}, 3*7200 + 300},                // range dominates fixed
	}
	for i, c := range cases {
		if got := uploadStaleThreshold(c.p); got != c.want {
			t.Errorf("case %d: got %d want %d", i, got, c.want)
		}
	}
}

func TestAgentUploadStale(t *testing.T) {
	now := time.Now().Unix() // isOnline uses real time; a fake far-future stamp reads as "online"
	online := store.Agent{ID: "a1", Enabled: true, LastSeenAt: now - 30}
	p := store.Policy{Enabled: true, IntervalSec: 60}

	// healthy: uploaded a minute ago
	if stale, _ := agentUploadStale(online, p, store.Stats{LastUploadAt: now - 60}, now); stale {
		t.Fatal("recent upload should not be stale")
	}
	// online but silent past the threshold
	stale, since := agentUploadStale(online, p, store.Stats{LastUploadAt: now - 46*60}, now)
	if !stale || since != 46*60 {
		t.Fatalf("silent agent: stale=%v since=%d, want true/%d", stale, since, 46*60)
	}
	// never uploaded: measured from creation
	createdLongAgo := online
	createdLongAgo.CreatedAt = now - 2*24*3600
	if stale, _ := agentUploadStale(createdLongAgo, p, store.Stats{}, now); !stale {
		t.Fatal("never-uploaded old node should be stale")
	}
	// fresh node that hasn't uploaded yet → not stale
	fresh := online
	fresh.CreatedAt = now - 60
	if stale, _ := agentUploadStale(fresh, p, store.Stats{}, now); stale {
		t.Fatal("brand-new node should not be stale")
	}
	// offline → never stale (control plane down is a different problem)
	offline := online
	offline.LastSeenAt = now - 3600
	if stale, _ := agentUploadStale(offline, p, store.Stats{LastUploadAt: now - 46*60}, now); stale {
		t.Fatal("offline agent should not flag upload_stale")
	}
	// policy paused → not expected to upload
	if stale, _ := agentUploadStale(online, store.Policy{Enabled: false, IntervalSec: 60}, store.Stats{}, now); stale {
		t.Fatal("paused policy should not be stale")
	}
}
