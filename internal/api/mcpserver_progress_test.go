package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateProgress(t *testing.T) {
	tests := []struct {
		name      string
		timeout   int
		progress  *MCPServerProgress
		wantError string
	}{
		{name: "no block", timeout: 120},
		{name: "block without maxDuration", timeout: 120, progress: &MCPServerProgress{}},
		{name: "equal to timeout", timeout: 120, progress: &MCPServerProgress{MaxDuration: 120}},
		{name: "between timeout and the limit", timeout: 120, progress: &MCPServerProgress{MaxDuration: 900}},
		{name: "at the limit", timeout: 120, progress: &MCPServerProgress{MaxDuration: DefaultProgressMaxDurationSeconds}},
		{name: "unset timeout counts as the default", progress: &MCPServerProgress{MaxDuration: 30}},
		{
			name:      "below timeout",
			timeout:   120,
			progress:  &MCPServerProgress{MaxDuration: 60},
			wantError: "progress.maxDuration must be between timeout (120) and 3600 seconds, got 60",
		},
		{
			name:      "below the default timeout",
			progress:  &MCPServerProgress{MaxDuration: 10},
			wantError: "progress.maxDuration must be between timeout (30) and 3600 seconds, got 10",
		},
		{
			name:      "above the limit",
			timeout:   120,
			progress:  &MCPServerProgress{MaxDuration: DefaultProgressMaxDurationSeconds + 1},
			wantError: "progress.maxDuration must be between timeout (120) and 3600 seconds, got 3601",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProgress(tt.timeout, tt.progress)
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantError)
		})
	}
}

func TestProgressMaxDuration(t *testing.T) {
	tests := []struct {
		name     string
		progress *MCPServerProgress
		want     time.Duration
	}{
		{name: "no block", want: time.Hour},
		{name: "block without maxDuration", progress: &MCPServerProgress{}, want: time.Hour},
		{name: "set", progress: &MCPServerProgress{MaxDuration: 900}, want: 15 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, (&MCPServer{Progress: tt.progress}).ProgressMaxDuration())
		})
	}
}
