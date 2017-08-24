package ddpool

import "time"

// HealthStatus is the representation of a node health.
type HealthStatus struct {
	Healthy      bool
	Held         bool
	Heartbeat    *time.Time
	ResponseTime time.Duration
	Error        error
}
