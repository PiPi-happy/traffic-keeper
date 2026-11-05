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
	AgentID        string
	Enabled        bool
	UploadEnabled  bool
	IntervalSec    int // fixed wait between uploads when range is unused
	IntervalMinSec int // when IntervalMaxSec>IntervalMinSec, agent randomizes the wait in [min,max]
	IntervalMaxSec int
	SizeMB         int // fixed size when range is unused
	SizeMinMB      int // when SizeMaxMB>SizeMinMB, agent randomizes in [min,max]
	SizeMaxMB      int
	UpdatedAt      int64
}

// AutomationConfig is the master-wide policy for independent agent downloads.
type AutomationConfig struct {
	Enabled          bool     `json:"enabled"`
	TargetMultiplier float64  `json:"target_multiplier"`
	WindowHours      int      `json:"window_hours"`
	Interface        string   `json:"interface"`
	CheckMinSec      int      `json:"check_min_sec"`
	CheckMaxSec      int      `json:"check_max_sec"`
	MaxTaskMB        int      `json:"max_task_mb"`
	Sources          []string `json:"sources"`
}

// AppSource is a configurable APK download source.
type AppSource struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Publisher    string `json:"publisher"`
	URL          string `json:"url"`
	Enabled      bool   `json:"enabled"`
	Official     bool   `json:"official"`
	AllowedHosts string `json:"allowed_hosts"`
	MaxMB        int    `json:"max_mb"`
	Verified     bool   `json:"verified"`
	VerifiedURL  string `json:"-"`
}

// AutomationEvent is one agent-side download task result.
type AutomationEvent struct {
	ID         int64  `json:"id"`
	AgentID    string `json:"agent_id"`
	SourceID   string `json:"source_id"`
	AppName    string `json:"app_name"`
	StartedAt  int64  `json:"started_at"`
	Bytes      int64  `json:"bytes"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	DurationMs int64  `json:"duration_ms"`
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
