package broker

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

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
