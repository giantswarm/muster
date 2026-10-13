package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateMaxCallDuration(t *testing.T) {
	tests := []struct {
		name            string
		timeout         int
		maxCallDuration int
		wantError       string
	}{
		{name: "unset", timeout: 120},
		{name: "equal to timeout", timeout: 120, maxCallDuration: 120},
		{name: "between timeout and the limit", timeout: 120, maxCallDuration: 900},
		{name: "at the limit", timeout: 120, maxCallDuration: MaxCallDurationLimit},
		{name: "unset timeout counts as the default", maxCallDuration: 30},
		{
			name:            "below timeout",
			timeout:         120,
			maxCallDuration: 60,
			wantError:       "maxCallDuration must be between timeout (120) and 3600 seconds, got 60",
		},
		{
			name:            "below the default timeout",
			maxCallDuration: 10,
			wantError:       "maxCallDuration must be between timeout (30) and 3600 seconds, got 10",
		},
		{
			name:            "above the limit",
			timeout:         120,
			maxCallDuration: MaxCallDurationLimit + 1,
			wantError:       "maxCallDuration must be between timeout (120) and 3600 seconds, got 3601",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMaxCallDuration(tt.timeout, tt.maxCallDuration)
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantError)
		})
	}
}
