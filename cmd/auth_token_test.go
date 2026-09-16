package cmd

import "testing"

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
