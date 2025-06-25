package capability

import (
	"time"
)

// ExecutionResult represents the result of executing an action
type ExecutionResult struct {
	Success bool                   `json:"success"`
	Data    map[string]interface{} `json:"data"`
	Error   string                 `json:"error,omitempty"`
	Steps   []StepResult           `json:"steps,omitempty"`
}

// StepResult represents the result of a single step in a multi-step action
type StepResult struct {
	StepID   int                    `json:"step_id"`
	Tool     string                 `json:"tool"`
	Success  bool                   `json:"success"`
	Data     map[string]interface{} `json:"data,omitempty"`
	Error    string                 `json:"error,omitempty"`
	Duration time.Duration          `json:"duration"`
}
