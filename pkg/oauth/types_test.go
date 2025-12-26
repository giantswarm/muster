package oauth

import (
	"testing"
	"time"
)

func TestToken_IsExpired(t *testing.T) {
	tests := []struct {
		name  string
		token *Token
		want  bool
	}{
		{
			name: "not expired",
			token: &Token{
				ExpiresAt: time.Now().Add(time.Hour),
			},
			want: false,
		},
		{
			name: "expired",
			token: &Token{
				ExpiresAt: time.Now().Add(-time.Hour),
			},
			want: true,
		},
		{
			name: "expires within margin",
			token: &Token{
				ExpiresAt: time.Now().Add(15 * time.Second), // Less than 30s margin
			},
			want: true,
		},
		{
			name: "no expiry set",
			token: &Token{
				ExpiresAt: time.Time{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.token.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToken_IsExpiredWithMargin(t *testing.T) {
	token := &Token{
		ExpiresAt: time.Now().Add(2 * time.Minute),
	}

	// With 1 minute margin, should not be expired
	if token.IsExpiredWithMargin(time.Minute) {
		t.Error("IsExpiredWithMargin(1m) = true, want false")
	}

	// With 3 minute margin, should be expired
	if !token.IsExpiredWithMargin(3 * time.Minute) {
		t.Error("IsExpiredWithMargin(3m) = false, want true")
	}
}

func TestToken_SetExpiresAtFromExpiresIn(t *testing.T) {
	tests := []struct {
		name      string
		token     *Token
		wantSet   bool
		tolerance time.Duration
	}{
		{
			name: "sets expiry from expires_in",
			token: &Token{
				ExpiresIn: 3600,
			},
			wantSet:   true,
			tolerance: 5 * time.Second,
		},
		{
			name: "does not override existing expiry",
			token: &Token{
				ExpiresIn: 3600,
				ExpiresAt: time.Now().Add(2 * time.Hour),
			},
			wantSet: false, // Should not change
		},
		{
			name: "zero expires_in",
			token: &Token{
				ExpiresIn: 0,
			},
			wantSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalExpiry := tt.token.ExpiresAt
			tt.token.SetExpiresAtFromExpiresIn()

			if tt.wantSet {
				if tt.token.ExpiresAt.IsZero() {
					t.Error("ExpiresAt was not set")
				}
				expected := time.Now().Add(time.Duration(tt.token.ExpiresIn) * time.Second)
				diff := tt.token.ExpiresAt.Sub(expected)
				if diff < -tt.tolerance || diff > tt.tolerance {
					t.Errorf("ExpiresAt = %v, want ~%v", tt.token.ExpiresAt, expected)
				}
			} else {
				if tt.token.ExpiresAt != originalExpiry {
					t.Errorf("ExpiresAt changed from %v to %v", originalExpiry, tt.token.ExpiresAt)
				}
			}
		})
	}
}

func TestToken_Scopes(t *testing.T) {
	tests := []struct {
		name  string
		token *Token
		want  []string
	}{
		{
			name:  "empty scope",
			token: &Token{Scope: ""},
			want:  nil,
		},
		{
			name:  "single scope",
			token: &Token{Scope: "openid"},
			want:  []string{"openid"},
		},
		{
			name:  "multiple scopes",
			token: &Token{Scope: "openid profile email"},
			want:  []string{"openid", "profile", "email"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.token.Scopes()
			if len(got) != len(tt.want) {
				t.Errorf("Scopes() = %v, want %v", got, tt.want)
				return
			}
			for i, s := range got {
				if s != tt.want[i] {
					t.Errorf("Scopes()[%d] = %q, want %q", i, s, tt.want[i])
				}
			}
		})
	}
}

func TestMetadata_SupportsPKCE(t *testing.T) {
	tests := []struct {
		name     string
		metadata *Metadata
		want     bool
	}{
		{
			name: "explicit S256 support",
			metadata: &Metadata{
				CodeChallengeMethodsSupported: []string{"plain", "S256"},
			},
			want: true,
		},
		{
			name: "only plain",
			metadata: &Metadata{
				CodeChallengeMethodsSupported: []string{"plain"},
			},
			want: false,
		},
		{
			name: "empty list assumes S256",
			metadata: &Metadata{
				CodeChallengeMethodsSupported: []string{},
			},
			want: true,
		},
		{
			name:     "nil list assumes S256",
			metadata: &Metadata{},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.metadata.SupportsPKCE(); got != tt.want {
				t.Errorf("SupportsPKCE() = %v, want %v", got, tt.want)
			}
		})
	}
}
