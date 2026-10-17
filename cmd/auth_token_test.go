package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/giantswarm/muster/v5/internal/api"
)

func TestAuthTokenCmdProperties(t *testing.T) {
	if authTokenCmd.Use != "token" {
		t.Errorf("expected Use 'token', got %q", authTokenCmd.Use)
	}
	if authTokenCmd.Short == "" || authTokenCmd.Long == "" {
		t.Error("expected Short and Long descriptions to be set")
	}
	if authTokenCmd.RunE == nil {
		t.Error("expected RunE to be set")
	}
	if authTokenCmd.Flags().Lookup("id") == nil {
		t.Error("expected token command to define --id")
	}

	registered := false
	for _, sub := range authCmd.Commands() {
		if sub == authTokenCmd {
			registered = true
		}
	}
	if !registered {
		t.Error("expected token command to be registered under auth")
	}
}

// tokenHandler answers GetAccessToken from its stored token, valid or not.
type tokenHandler struct {
	api.AuthHandler
	token string
	valid bool
}

func (h *tokenHandler) GetAccessToken(string) (string, error) {
	if !h.valid {
		return "", errors.New("authentication required")
	}
	return h.token, nil
}

func TestCurrentAccessToken(t *testing.T) {
	const endpoint = "https://muster.example.com/mcp"
	gatewayDown := errors.New("failed to connect to aggregator: request failed with status 500: upstream call failed: Connect: deadline has elapsed")

	tests := []struct {
		name       string
		valid      bool
		renewErr   error
		renewed    bool // the renewal makes the stored token valid
		wantToken  string
		wantRenews int
		wantErr    string
	}{
		{name: "valid token needs no aggregator", valid: true, renewErr: gatewayDown, wantToken: "access"},
		{name: "expired token is renewed", renewed: true, wantToken: "access", wantRenews: 1},
		{name: "renewal rejected", renewErr: transport.ErrUnauthorized, wantRenews: 1, wantErr: "run 'muster auth login' first"},
		{name: "renewal unreachable", renewErr: gatewayDown, wantRenews: 1, wantErr: "upstream call failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &tokenHandler{token: "access", valid: tt.valid}
			renews := 0
			got, err := currentAccessToken(h, endpoint, func() error {
				renews++
				if tt.renewed {
					h.valid = true
				}
				return tt.renewErr
			})
			if renews != tt.wantRenews {
				t.Errorf("renewals = %d, want %d", renews, tt.wantRenews)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.wantToken {
				t.Fatalf("currentAccessToken = %q, %v; want %q", got, err, tt.wantToken)
			}
		})
	}
}
