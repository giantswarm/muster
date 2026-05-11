package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestContextWithIDToken_RoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		wantToken string
		wantOK    bool
	}{
		{"non-empty token round-trips", "id-token-1", "id-token-1", true},
		{"empty token is treated as absent", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ContextWithIDToken(context.Background(), tc.token)
			got, ok := GetIDTokenFromContext(ctx)
			require.Equal(t, tc.wantToken, got)
			require.Equal(t, tc.wantOK, ok)
		})
	}
}

func TestGetIDTokenFromContext_AbsentReturnsFalse(t *testing.T) {
	_, ok := GetIDTokenFromContext(context.Background())
	require.False(t, ok)
}

func TestGetIDToken(t *testing.T) {
	tests := []struct {
		name  string
		token *oauth2.Token
		want  string
	}{
		{"nil token returns empty", nil, ""},
		{"token without id_token extra", &oauth2.Token{AccessToken: "a"}, ""},
		{
			"token with id_token extra",
			(&oauth2.Token{AccessToken: "a"}).WithExtra(map[string]any{"id_token": "id-token-1"}),
			"id-token-1",
		},
		{
			"id_token extra of wrong type returns empty",
			(&oauth2.Token{AccessToken: "a"}).WithExtra(map[string]any{"id_token": 42}),
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, GetIDToken(tc.token))
		})
	}
}
