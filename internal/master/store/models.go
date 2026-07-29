package store

import "time"

// Agent is a registered sender node.
type Agent struct {
	ID             string
	Name           string
	Token          string // one-time token embedded in the install command
	Secret         string // secret the agent uses to authenticate API calls
	Enabled        bool
	CreatedAt      int64 // unix seconds
	LastSeenAt     int64 // unix seconds
	LastIP         string
	Version        string // agent-reported version
	PendingUpgrade string // target version the agent should self-upgrade to ("" = none)
	Country        string // agent-reported ISO country code (e.g. CN, US)
	Arch           string // agent-reported GOARCH (amd64 / arm64)
}

// Policy is the per-agent upload schedule.
type Policy struct {
	AgentID     string
	Enabled     bool
	IntervalSec int
	SizeMB      int // fixed size when range is unused
	SizeMinMB   int // when SizeMaxMB>SizeMinMB, agent randomizes in [min,max]
	SizeMaxMB   int
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
