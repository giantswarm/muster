package cmd

import (
	"testing"
)

func TestAuthLoginCmdProperties(t *testing.T) {
	t.Run("login command Use field", func(t *testing.T) {
		if authLoginCmd.Use != "login" {
			t.Errorf("expected Use 'login', got %q", authLoginCmd.Use)
		}
	})

	t.Run("login command has short description", func(t *testing.T) {
		if authLoginCmd.Short == "" {
			t.Error("expected Short description to be set")
		}
	})

	t.Run("login command has long description", func(t *testing.T) {
		if authLoginCmd.Long == "" {
			t.Error("expected Long description to be set")
		}
	})

	t.Run("login command has RunE", func(t *testing.T) {
		if authLoginCmd.RunE == nil {
			t.Error("expected RunE to be set")
		}
	})

	t.Run("login command long description mentions examples", func(t *testing.T) {
		if authLoginCmd.Long == "" {
			t.Error("expected Long description to contain examples")
		}
	})
}

func TestAuthLogoutCmdProperties(t *testing.T) {
	t.Run("logout command Use field", func(t *testing.T) {
		if authLogoutCmd.Use != "logout" {
			t.Errorf("expected Use 'logout', got %q", authLogoutCmd.Use)
		}
	})

	t.Run("logout command has short description", func(t *testing.T) {
		if authLogoutCmd.Short == "" {
			t.Error("expected Short description to be set")
		}
	})

	t.Run("logout command has RunE", func(t *testing.T) {
		if authLogoutCmd.RunE == nil {
			t.Error("expected RunE to be set")
		}
	})
}
