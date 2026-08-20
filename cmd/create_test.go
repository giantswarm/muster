package cmd

import (
	"reflect"
	"testing"
)

func TestProcessMCPServerFlagAuth(t *testing.T) {
	tests := []struct {
		name     string
		flags    [][3]string // flagName, flagValue, hasValue ("1" = true)
		expected map[string]interface{}
	}{
		{
			name:  "auth type oauth",
			flags: [][3]string{{"auth-type", "oauth", "1"}},
			expected: map[string]interface{}{
				"auth": map[string]interface{}{"type": "oauth"},
			},
		},
		{
			name: "oauth with pinned authorization server",
			flags: [][3]string{
				{"auth-type", "oauth", "1"},
				{"auth-issuer", "https://auth.example.com", "1"},
				{"auth-scopes", "openid profile", "1"},
			},
			expected: map[string]interface{}{
				"auth": map[string]interface{}{
					"type": "oauth",
					"authorizationServer": map[string]interface{}{
						"issuer": "https://auth.example.com",
						"scopes": "openid profile",
					},
				},
			},
		},
		{
			name:  "bare forward-token defaults to true",
			flags: [][3]string{{"forward-token", "", ""}},
			expected: map[string]interface{}{
				"auth": map[string]interface{}{"forwardToken": true},
			},
		},
		{
			name:  "forward-token explicit false",
			flags: [][3]string{{"forward-token", "false", "1"}},
			expected: map[string]interface{}{
				"auth": map[string]interface{}{"forwardToken": false},
			},
		},
		{
			name: "forward-token with required audiences",
			flags: [][3]string{
				{"forward-token", "true", "1"},
				{"required-audiences", "dex-k8s-authenticator, other-audience", "1"},
			},
			expected: map[string]interface{}{
				"auth": map[string]interface{}{
					"forwardToken":      true,
					"requiredAudiences": []string{"dex-k8s-authenticator", "other-audience"},
				},
			},
		},
		{
			name: "camelCase aliases",
			flags: [][3]string{
				{"authType", "oauth", "1"},
				{"authIssuer", "https://auth.example.com", "1"},
				{"forwardToken", "true", "1"},
				{"requiredAudiences", "aud1", "1"},
			},
			expected: map[string]interface{}{
				"auth": map[string]interface{}{
					"type":              "oauth",
					"forwardToken":      true,
					"requiredAudiences": []string{"aud1"},
					"authorizationServer": map[string]interface{}{
						"issuer": "https://auth.example.com",
					},
				},
			},
		},
		{
			name:     "no auth flags leaves auth unset",
			flags:    [][3]string{{"url", "https://api.example.com/mcp", "1"}},
			expected: map[string]interface{}{"url": "https://api.example.com/mcp"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]interface{}{}
			for _, f := range tt.flags {
				processMCPServerFlag(args, f[0], f[1], f[2] == "1")
			}
			if !reflect.DeepEqual(args, tt.expected) {
				t.Errorf("got %#v, want %#v", args, tt.expected)
			}
		})
	}
}
