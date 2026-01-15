package agent

import (
	"strings"
	"testing"

	pkgoauth "muster/pkg/oauth"

	"github.com/mark3labs/mcp-go/mcp"
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

func TestAuthMetaKey_Namespacing(t *testing.T) {
	// Verify the auth meta key follows the expected namespacing convention
	if AuthMetaKey != "giantswarm.io/auth_required" {
		t.Errorf("expected AuthMetaKey to be 'giantswarm.io/auth_required', got '%s'", AuthMetaKey)
	}

	// Verify it starts with the company namespace
	if !strings.HasPrefix(AuthMetaKey, "giantswarm.io/") {
		t.Error("AuthMetaKey should be namespaced under 'giantswarm.io/'")
	}
}

// mockAuthPoller is a mock implementation for testing
type mockAuthPoller struct {
	authRequired []pkgoauth.AuthRequiredInfo
}

func (m *mockAuthPoller) GetAuthRequired() []pkgoauth.AuthRequiredInfo {
	return m.authRequired
}

func (m *mockAuthPoller) HasAuthRequired() bool {
	return len(m.authRequired) > 0
}

func TestWrapToolResultWithAuth_NoAuthRequired(t *testing.T) {
	// Create a minimal MCPServer with a mock poller that has no auth required
	m := &MCPServer{
		authPoller: &authPoller{
			cache: []pkgoauth.AuthRequiredInfo{}, // Empty cache
		},
	}

	// Create a simple tool result
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: "Tool executed successfully",
			},
		},
	}

	// Wrap the result
	wrapped := m.wrapToolResultWithAuth(result)

	// Result should be unchanged when no auth is required
	if len(wrapped.Content) != 1 {
		t.Errorf("expected 1 content item, got %d", len(wrapped.Content))
	}

	// Meta should be nil or empty
	if wrapped.Meta != nil && len(wrapped.Meta.AdditionalFields) > 0 {
		t.Error("Meta should be nil or empty when no auth is required")
	}
}

func TestWrapToolResultWithAuth_WithAuthRequired(t *testing.T) {
	// Create a MCPServer with auth required
	m := &MCPServer{
		authPoller: &authPoller{
			cache: []pkgoauth.AuthRequiredInfo{
				{
					Server:   "gitlab",
					Issuer:   "https://dex.example.com",
					Scope:    "openid",
					AuthTool: "x_gitlab_authenticate",
				},
			},
		},
	}

	// Create a simple tool result
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: "Tool executed successfully",
			},
		},
	}

	// Wrap the result
	wrapped := m.wrapToolResultWithAuth(result)

	// Should have additional content with auth notification
	if len(wrapped.Content) != 2 {
		t.Errorf("expected 2 content items, got %d", len(wrapped.Content))
	}

	// Verify notification content
	if len(wrapped.Content) >= 2 {
		textContent, ok := wrapped.Content[1].(mcp.TextContent)
		if !ok {
			t.Error("second content item should be TextContent")
		} else {
			if !strings.Contains(textContent.Text, "Authentication Required") {
				t.Error("notification should contain 'Authentication Required'")
			}
			if !strings.Contains(textContent.Text, "gitlab") {
				t.Error("notification should mention the server")
			}
		}
	}

	// Meta should contain auth_required
	if wrapped.Meta == nil {
		t.Fatal("Meta should not be nil when auth is required")
	}
	if wrapped.Meta.AdditionalFields == nil {
		t.Fatal("Meta.AdditionalFields should not be nil")
	}
	authData, ok := wrapped.Meta.AdditionalFields[AuthMetaKey]
	if !ok {
		t.Fatalf("Meta should contain key '%s'", AuthMetaKey)
	}

	authList, ok := authData.([]pkgoauth.AuthRequiredInfo)
	if !ok {
		t.Fatal("auth_required should be a slice of AuthRequiredInfo")
	}
	if len(authList) != 1 {
		t.Errorf("expected 1 auth required entry, got %d", len(authList))
	}
}

func TestWrapToolResultWithAuth_MetaFieldStructure(t *testing.T) {
	// Create a MCPServer with multiple servers requiring auth
	m := &MCPServer{
		authPoller: &authPoller{
			cache: []pkgoauth.AuthRequiredInfo{
				{
					Server:   "gitlab",
					Issuer:   "https://dex.example.com",
					Scope:    "openid",
					AuthTool: "x_gitlab_authenticate",
				},
				{
					Server:   "jira",
					Issuer:   "https://dex.example.com",
					Scope:    "openid profile",
					AuthTool: "x_jira_authenticate",
				},
			},
		},
	}

	// Create a simple tool result
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: "Original result",
			},
		},
	}

	// Wrap the result
	wrapped := m.wrapToolResultWithAuth(result)

	// Verify Meta structure
	if wrapped.Meta == nil {
		t.Fatal("Meta should not be nil")
	}

	authData, ok := wrapped.Meta.AdditionalFields[AuthMetaKey]
	if !ok {
		t.Fatalf("Meta should contain '%s'", AuthMetaKey)
	}

	authList, ok := authData.([]pkgoauth.AuthRequiredInfo)
	if !ok {
		t.Fatal("auth_required should be a slice of AuthRequiredInfo")
	}

	if len(authList) != 2 {
		t.Errorf("expected 2 auth required entries, got %d", len(authList))
	}

	// Verify each entry has the required fields
	for i, auth := range authList {
		if auth.Server == "" {
			t.Errorf("entry %d: Server should not be empty", i)
		}
		if auth.AuthTool == "" {
			t.Errorf("entry %d: AuthTool should not be empty", i)
		}
	}
}

func TestWrapToolResultWithAuth_NilAuthPoller(t *testing.T) {
	// Create a MCPServer with nil authPoller
	m := &MCPServer{
		authPoller: nil,
	}

	// Create a simple tool result
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: "Tool result",
			},
		},
	}

	// Should not panic and return original result
	wrapped := m.wrapToolResultWithAuth(result)

	if len(wrapped.Content) != 1 {
		t.Errorf("expected 1 content item, got %d", len(wrapped.Content))
	}
}

func TestWrapToolResultWithAuth_PreservesExistingMeta(t *testing.T) {
	// Create a MCPServer with auth required
	m := &MCPServer{
		authPoller: &authPoller{
			cache: []pkgoauth.AuthRequiredInfo{
				{
					Server:   "test-server",
					Issuer:   "https://idp.example.com",
					AuthTool: "x_test-server_authenticate",
				},
			},
		},
	}

	// Create a tool result with existing meta
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: "Result",
			},
		},
	}
	result.Meta = &mcp.Meta{
		AdditionalFields: map[string]any{
			"existing_key": "existing_value",
		},
	}

	// Wrap the result
	wrapped := m.wrapToolResultWithAuth(result)

	// Should preserve existing meta
	if wrapped.Meta.AdditionalFields["existing_key"] != "existing_value" {
		t.Error("existing meta field should be preserved")
	}

	// Should also have auth_required
	if _, ok := wrapped.Meta.AdditionalFields[AuthMetaKey]; !ok {
		t.Errorf("should have added '%s' to meta", AuthMetaKey)
	}
}
