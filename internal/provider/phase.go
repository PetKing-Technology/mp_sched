package provider

// RuntimeStatus 阶段常量，与 Provider 实现约定一致
const (
	PhaseRunning   = "running"
	PhaseSucceeded = "succeeded"
	PhaseFailed    = "failed"
	PhaseStopped   = "stopped"
	PhaseUnknown   = "unknown"
)
