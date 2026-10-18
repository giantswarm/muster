package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// authURLsArg asks core_auth_status for the sign-in link of every server that
// waits for the person's grant.
const authURLsArg = "auth_urls"

// handleAuthStatus answers core_auth_status: the caller's state on every
// registered server, as auth://status reports it, and with auth_urls the
// sign-in link that starts each missing grant. A client that gets a person
// ready before the agent needs them (a chat front-end after sign-in) reads
// everything it shows in one call instead of a core_auth_login per server.
//
// Issuing the links is one authentication attempt for the rate limit, and
// each link is the one core_auth_login would issue: a server whose grant the
// person already holds is connected instead and reported connected.
func (p *AuthToolProvider) handleAuthStatus(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	withURLs, _ := args[authURLsArg].(bool)

	sessionID, sub, errResult := requireSessionContextResult(ctx)
	if errResult != nil {
		return errResult, nil
	}

	response := p.aggregator.sessionAuthStatus(ctx)
	if withURLs && awaitsGrant(response.Servers) {
		if refused := p.rateLimited(sub, "core_auth_status"); refused != nil {
			return refused, nil
		}
		for i := range response.Servers {
			p.issueAuthURL(ctx, sessionID, sub, &response.Servers[i])
		}
	}

	data, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encoding the auth status: %w", err)
	}
	return &api.CallToolResult{
		Content:           []any{string(data)},
		StructuredContent: response,
	}, nil
}

// awaitsGrant reports whether any server waits for a grant core_auth_login can
// start.
func awaitsGrant(servers []pkgoauth.ServerAuthStatus) bool {
	for _, s := range servers {
		if grantable(s) {
			return true
		}
	}
	return false
}

// grantable reports whether core_auth_login can start the server's grant now.
func grantable(s pkgoauth.ServerAuthStatus) bool {
	if s.AuthTool == "" || s.Suspended {
		return false
	}
	return s.Status == pkgoauth.SessionServerStatusAuthRequired || s.Status == pkgoauth.SessionServerStatusReauthRequired
}

// issueAuthURL fills the entry's sign-in link the way core_auth_login does. A
// login that connects with a grant the person already holds turns the entry
// connected; a refusal is the entry's error.
func (p *AuthToolProvider) issueAuthURL(ctx context.Context, sessionID, sub string, entry *pkgoauth.ServerAuthStatus) {
	if !grantable(*entry) {
		return
	}
	result := p.login(ctx, sessionID, sub, entry.Name, false, nil)
	if url := challengeURL(result); url != "" {
		entry.AuthURL = url
		return
	}
	if result.IsError {
		entry.Error = resultText(result)
		return
	}
	entry.Status = pkgoauth.SessionServerStatusConnected
	entry.AuthTool = ""
}

// challengeURL is the sign-in link of a core_auth_login challenge, or empty.
func challengeURL(result *api.CallToolResult) string {
	if result == nil || result.IsError {
		return ""
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		return ""
	}
	url, _ := structured["authUrl"].(string)
	return url
}

// resultText is the text of a tool result, its parts on separate lines.
func resultText(result *api.CallToolResult) string {
	var parts []string
	for _, c := range result.Content {
		if text, ok := c.(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
