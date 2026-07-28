package store

import "time"

// Agent is a registered sender node.
type Agent struct {
	ID         string
	Name       string
	Token      string // one-time token embedded in the install command
	Secret     string // secret the agent uses to authenticate API calls
	Enabled    bool
	CreatedAt  int64 // unix seconds
	LastSeenAt int64 // unix seconds
	LastIP     string
}

// Policy is the per-agent upload schedule.
type Policy struct {
	AgentID     string
	Enabled     bool
	IntervalSec int
	SizeMB      int
	UpdatedAt   int64
}

// Stats holds the accumulated upstream counters for an agent.
type Stats struct {
	AgentID      string
	BytesUp      int64
	UploadCount  int64
	LastUploadAt int64
}

func now() int64 { return time.Now().Unix() }

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
