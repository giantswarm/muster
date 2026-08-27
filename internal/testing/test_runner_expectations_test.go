package testing

import "testing"

// TestValidateTestToolExpectationsNotContains covers not_contains on test_*
// steps. It used to be accepted by the scenario loader but never evaluated by
// the test-tool path, so every absence assertion on a test_* step passed
// vacuously — including the session-isolation ones that guard against one
// user's authenticated backend leaking into another user's session.
func TestValidateTestToolExpectationsNotContains(t *testing.T) {
	response := map[string]interface{}{
		"success":    true,
		"tool_count": 2,
		"tools":      []string{"core_auth_login", "x_server-alpha_alpha_tool"},
	}

	tests := []struct {
		name     string
		expected TestExpectation
		want     bool
	}{
		{
			name:     "absent text passes",
			expected: TestExpectation{Success: true, NotContains: []string{"x_server-beta"}},
			want:     true,
		},
		{
			name:     "present text fails",
			expected: TestExpectation{Success: true, NotContains: []string{"x_server-alpha"}},
			want:     false,
		},
		{
			name:     "fails when any entry is present",
			expected: TestExpectation{Success: true, NotContains: []string{"x_server-beta", "x_server-alpha"}},
			want:     false,
		},
		{
			name: "combines with contains",
			expected: TestExpectation{
				Success:     true,
				Contains:    []string{"core_auth_login"},
				NotContains: []string{"x_server-beta"},
			},
			want: true,
		},
	}

	runner := &testRunner{logger: NewSilentLogger(false, false)}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runner.validateTestToolExpectations(tt.expected, response, nil, runner.logger)
			if got != tt.want {
				t.Errorf("validateTestToolExpectations() = %v, want %v", got, tt.want)
			}
		})
	}
}
