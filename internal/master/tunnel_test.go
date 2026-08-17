package master

import (
	"testing"
	"time"
)

func TestSelfHealNeeded(t *testing.T) {
	tm := newTunnelManager()

	// not enabled → never needed
	if tm.selfHealNeeded() {
		t.Fatal("disabled tunnel should not self-heal")
	}

	// enabled but healthy → not needed
	tm.mu.Lock()
	tm.enabled = true
	tm.mu.Unlock()
	if tm.selfHealNeeded() {
		t.Fatal("healthy tunnel should not self-heal")
	}

	// enabled, rejected but below threshold → not yet
	tm.mu.Lock()
	tm.consecNotFound = selfHealNotFoundThreshold - 1
	tm.mu.Unlock()
	if tm.selfHealNeeded() {
		t.Fatal("below threshold should not self-heal")
	}

	// enabled + threshold reached + no recent heal → needed
	tm.mu.Lock()
	tm.consecNotFound = selfHealNotFoundThreshold
	tm.mu.Unlock()
	if !tm.selfHealNeeded() {
		t.Fatal("threshold reached should self-heal")
	}

	// inside cooldown after a recent heal → not needed
	tm.mu.Lock()
	tm.lastSelfHeal = time.Now()
	tm.mu.Unlock()
	if tm.selfHealNeeded() {
		t.Fatal("inside cooldown should not self-heal")
	}

	// cooldown elapsed → needed again
	tm.mu.Lock()
	tm.lastSelfHeal = time.Now().Add(-selfHealCooldown - time.Minute)
	tm.mu.Unlock()
	if !tm.selfHealNeeded() {
		t.Fatal("after cooldown should self-heal")
	}
}

// The scanner counting logic (Tunnel not found ++ / Registered reset) can't be
// exercised without a real cloudflared, but Disable must clear the counter so
// a restart never inherits stale rejections.
func TestDisableResetsNotFoundCounter(t *testing.T) {
	tm := newTunnelManager()
	tm.mu.Lock()
	tm.enabled = true
	tm.consecNotFound = 5
	tm.mu.Unlock()
	if err := tm.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	tm.mu.Lock()
	n := tm.consecNotFound
	e := tm.enabled
	tm.mu.Unlock()
	if n != 0 || e {
		t.Fatalf("after disable: consecNotFound=%d enabled=%v, want 0/false", n, e)
	}
}
