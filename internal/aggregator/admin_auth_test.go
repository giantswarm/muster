package aggregator

import "testing"

func TestDecideAdminAuth(t *testing.T) {
	tests := []struct {
		name             string
		hasOAuthListener bool
		oauthEnabled     bool
		want             adminAuthMode
	}{
		{
			name:             "oauth listener present wires validation",
			hasOAuthListener: true,
			oauthEnabled:     true,
			want:             adminAuthOAuth,
		},
		{
			name:             "oauth enabled but no listener refuses to start",
			hasOAuthListener: false,
			oauthEnabled:     true,
			want:             adminAuthUnavailable,
		},
		{
			name:             "oauth disabled serves loopback only",
			hasOAuthListener: false,
			oauthEnabled:     false,
			want:             adminAuthLoopbackOnly,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideAdminAuth(tc.hasOAuthListener, tc.oauthEnabled); got != tc.want {
				t.Fatalf("decideAdminAuth(%v, %v) = %d, want %d",
					tc.hasOAuthListener, tc.oauthEnabled, got, tc.want)
			}
		})
	}
}
