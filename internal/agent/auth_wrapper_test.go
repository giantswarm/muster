package agent

import (
	"strings"
	"testing"

	pkgoauth "muster/pkg/oauth"
)

func TestBuildAuthNotification_SingleServer(t *testing.T) {
	authRequired := []pkgoauth.AuthRequiredInfo{
		{
			Server:   "gitlab",
			Issuer:   "https://dex.example.com",
			Scope:    "openid",
			AuthTool: "x_gitlab_authenticate",
		},
	}

	notification := buildAuthNotification(authRequired)

	// Should contain the server name
	if !strings.Contains(notification, "gitlab") {
		t.Error("notification should contain server name 'gitlab'")
	}

	// Should contain the auth tool
	if !strings.Contains(notification, "x_gitlab_authenticate") {
		t.Error("notification should contain auth tool name")
	}

	// Should contain "Authentication Required"
	if !strings.Contains(notification, "Authentication Required") {
		t.Error("notification should contain 'Authentication Required'")
	}

	// Should NOT contain SSO hint (only one server)
	if strings.Contains(notification, "same identity provider") {
		t.Error("notification should not contain SSO hint for single server")
	}
}

func TestBuildAuthNotification_MultipleServersWithSSO(t *testing.T) {
	authRequired := []pkgoauth.AuthRequiredInfo{
		{
			Server:   "gitlab",
			Issuer:   "https://dex.example.com",
			Scope:    "openid",
			AuthTool: "x_gitlab_authenticate",
		},
		{
			Server:   "jira",
			Issuer:   "https://dex.example.com", // Same issuer = SSO
			Scope:    "openid",
			AuthTool: "x_jira_authenticate",
		},
	}

	notification := buildAuthNotification(authRequired)

	// Should contain both server names
	if !strings.Contains(notification, "gitlab") {
		t.Error("notification should contain 'gitlab'")
	}
	if !strings.Contains(notification, "jira") {
		t.Error("notification should contain 'jira'")
	}

	// Should contain SSO hint
	if !strings.Contains(notification, "same identity provider") {
		t.Error("notification should contain SSO hint")
	}

	// Should mention the issuer in SSO hint
	if !strings.Contains(notification, "dex.example.com") {
		t.Error("notification should mention the issuer")
	}
}

func TestBuildAuthNotification_MultipleServersDifferentIssuers(t *testing.T) {
	authRequired := []pkgoauth.AuthRequiredInfo{
		{
			Server:   "gitlab",
			Issuer:   "https://dex.example.com",
			Scope:    "openid",
			AuthTool: "x_gitlab_authenticate",
		},
		{
			Server:   "github",
			Issuer:   "https://github.com/oauth", // Different issuer
			Scope:    "repo",
			AuthTool: "x_github_authenticate",
		},
	}

	notification := buildAuthNotification(authRequired)

	// Should contain both server names
	if !strings.Contains(notification, "gitlab") {
		t.Error("notification should contain 'gitlab'")
	}
	if !strings.Contains(notification, "github") {
		t.Error("notification should contain 'github'")
	}

	// Should NOT contain SSO hint (different issuers)
	if strings.Contains(notification, "same identity provider") {
		t.Error("notification should not contain SSO hint for different issuers")
	}
}

func TestBuildAuthNotification_EmptyIssuer(t *testing.T) {
	authRequired := []pkgoauth.AuthRequiredInfo{
		{
			Server:   "legacy-server",
			Issuer:   "", // No issuer
			Scope:    "",
			AuthTool: "x_legacy-server_authenticate",
		},
	}

	notification := buildAuthNotification(authRequired)

	// Should still contain the server name
	if !strings.Contains(notification, "legacy-server") {
		t.Error("notification should contain server name")
	}

	// Should NOT cause SSO hint (no issuer)
	if strings.Contains(notification, "same identity provider") {
		t.Error("notification should not contain SSO hint when issuer is empty")
	}
}

func TestAuthRequiredInfo_JSON(t *testing.T) {
	info := pkgoauth.AuthRequiredInfo{
		Server:   "test-server",
		Issuer:   "https://idp.example.com",
		Scope:    "openid profile",
		AuthTool: "x_test-server_authenticate",
	}

	// Verify fields are accessible
	if info.Server != "test-server" {
		t.Errorf("expected server 'test-server', got '%s'", info.Server)
	}
	if info.Issuer != "https://idp.example.com" {
		t.Errorf("expected issuer 'https://idp.example.com', got '%s'", info.Issuer)
	}
}
