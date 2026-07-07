package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/mark3labs/mcp-go/mcp"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

const (
	// TestToolMintToken mints an ES256-signed JWT on a mock OAuth server and stores
	// it under a name for later use as a broker subject/actor token.
	TestToolMintToken = "test_mint_token"
	// TestToolBrokerTokenExchange performs an RFC 8693 token exchange against
	// muster's /oauth/token broker and verifies the minted JWT against muster's JWKS.
	TestToolBrokerTokenExchange = "test_broker_token_exchange"
	// TestToolCallProtectedMCP calls a tool on a protected mock MCP server with a
	// previously minted bearer token, proving downstream acceptance end-to-end.
	TestToolCallProtectedMCP = "test_call_protected_mcp"
	// TestToolReconnectWithToken reconnects the caller's MCP client to muster using
	// a previously minted trusted-issuer token as the bearer, simulating an agent
	// that forwards an external (Dex/SA) subject token. This lets data-plane
	// local-mint use the inbound bearer as the subject for per-call backend tokens.
	TestToolReconnectWithToken = "test_reconnect_with_token"
	// TestToolReconnectWithOBO reconnects the caller's MCP client to muster with a
	// subject bearer token and a static X-Actor-Token for RFC 8693 OBO delegation.
	TestToolReconnectWithOBO = "test_reconnect_with_obo"

	keySuccess = "success"

	// jwtTokenTypeURN is the RFC 8693 token-type identifier for JWTs.
	jwtTokenTypeURN = "urn:ietf:params:oauth:token-type:jwt" //nolint:gosec
)

// handleReconnectWithToken reconnects the caller's MCP client to muster with a
// previously minted token as the bearer. muster accepts it via TrustedIssuers and
// the data-plane local-mint path uses it as the subject token. args: token_ref.
func (h *TestToolsHandler) handleReconnectWithToken(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if h.currentInstance == nil {
		return nil, fmt.Errorf("current instance not available")
	}
	tokenRef, _ := args["token_ref"].(string)
	if tokenRef == "" {
		return nil, fmt.Errorf("token_ref argument is required")
	}
	token, ok := h.mintedTokens[tokenRef]
	if !ok {
		return nil, fmt.Errorf("no minted token named %q", tokenRef)
	}

	if h.mcpClient != nil {
		_ = h.mcpClient.Close()
	}
	newClient := NewMCPTestClientWithLogger(h.debug, h.logger)
	if err := newClient.ConnectWithAuth(ctx, h.currentInstance.Endpoint, token); err != nil {
		return nil, fmt.Errorf("reconnect with token failed: %w", err)
	}
	h.mcpClient = newClient
	h.userClients[defaultTestUser] = newClient
	h.currentUser = defaultTestUser

	return map[string]interface{}{keySuccess: true}, nil
}

// handleReconnectWithOBO reconnects with a subject bearer token and a static
// X-Actor-Token header for RFC 8693 OBO delegation. args: token_ref, actor_token_ref.
func (h *TestToolsHandler) handleReconnectWithOBO(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if h.currentInstance == nil {
		return nil, fmt.Errorf("current instance not available")
	}
	tokenRef, _ := args["token_ref"].(string)
	if tokenRef == "" {
		return nil, fmt.Errorf("token_ref argument is required")
	}
	actorRef, _ := args["actor_token_ref"].(string)
	if actorRef == "" {
		return nil, fmt.Errorf("actor_token_ref argument is required")
	}
	subjectToken, ok := h.mintedTokens[tokenRef]
	if !ok {
		return nil, fmt.Errorf("no minted token named %q", tokenRef)
	}
	actorToken, ok := h.mintedTokens[actorRef]
	if !ok {
		return nil, fmt.Errorf("no minted token named %q", actorRef)
	}

	if h.mcpClient != nil {
		_ = h.mcpClient.Close()
	}
	newClient := NewMCPTestClientWithLogger(h.debug, h.logger)
	if err := newClient.ConnectWithOBO(ctx, h.currentInstance.Endpoint, subjectToken, actorToken); err != nil {
		return nil, fmt.Errorf("reconnect with OBO failed: %w", err)
	}
	h.mcpClient = newClient
	h.userClients[defaultTestUser] = newClient
	h.currentUser = defaultTestUser

	return map[string]interface{}{keySuccess: true}, nil
}

// handleMintToken mints a signed JWT on the referenced mock OAuth server and
// stores it under args["name"] so later steps can present it as a subject or
// actor token. args: server, name, sub (required); iss, aud, typ (string),
// groups ([]string), act (map) optional.
func (h *TestToolsHandler) handleMintToken(_ context.Context, args map[string]interface{}) (interface{}, error) {
	if h.currentInstance == nil || h.instanceManager == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}

	serverName, _ := args["server"].(string)
	if serverName == "" {
		return nil, fmt.Errorf("server argument is required")
	}
	name, _ := args["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("name argument is required")
	}
	sub, _ := args["sub"].(string)
	if sub == "" {
		return nil, fmt.Errorf("sub argument is required")
	}

	srv := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, serverName)
	if srv == nil {
		return nil, fmt.Errorf("mock OAuth server %q not running", serverName)
	}

	claims := map[string]any{"sub": sub}
	if iss, ok := args["iss"].(string); ok && iss != "" {
		claims["iss"] = iss
	}
	// Default the audience to muster's own issuer: the broker binds workload
	// subject tokens (no-actor) and all actor tokens to its issuer as anti-replay
	// protection when the trusted issuer sets no allowedAudiences. The muster
	// port is dynamic, so resolve it here rather than hardcoding it in YAML.
	if aud, ok := args["aud"].(string); ok && aud != "" {
		claims["aud"] = aud
	} else {
		claims["aud"] = pkgoauth.NormalizeServerURL(h.currentInstance.Endpoint)
	}
	if groups := toStringSlice(args["groups"]); len(groups) > 0 {
		claims["groups"] = groups
	}
	if act, ok := args["act"].(map[string]interface{}); ok && len(act) > 0 {
		claims["act"] = act
	}
	typHeader, _ := args["typ"].(string)

	token, err := srv.MintSignedJWT(claims, typHeader)
	if err != nil {
		return nil, fmt.Errorf("minting token: %w", err)
	}
	h.mintedTokens[name] = token

	return map[string]interface{}{
		keySuccess: true,
		"name":     name,
		"subject":  sub,
	}, nil
}

// handleBrokerTokenExchange POSTs an RFC 8693 token-exchange request to muster's
// /oauth/token broker (workload path, no client credentials) and, on success,
// verifies the minted JWT against muster's JWKS and returns its claims. On
// failure it returns the OAuth error so negative scenarios can assert on it.
// args: subject_token_ref, audience (required); actor_token_ref,
// subject_token_type, resource, name optional.
func (h *TestToolsHandler) handleBrokerTokenExchange(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if h.currentInstance == nil {
		return nil, fmt.Errorf("current instance not available")
	}

	subjectRef, _ := args["subject_token_ref"].(string)
	if subjectRef == "" {
		return nil, fmt.Errorf("subject_token_ref argument is required")
	}
	subjectToken, ok := h.mintedTokens[subjectRef]
	if !ok {
		return nil, fmt.Errorf("no minted token named %q (mint it with test_mint_token first)", subjectRef)
	}
	audience, _ := args["audience"].(string)
	if audience == "" {
		return nil, fmt.Errorf("audience argument is required")
	}

	subjectTokenType, _ := args["subject_token_type"].(string)
	if subjectTokenType == "" {
		subjectTokenType = jwtTokenTypeURN
	}

	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {subjectToken},
		"subject_token_type": {subjectTokenType},
		"audience":           {audience},
	}
	if actorRef, _ := args["actor_token_ref"].(string); actorRef != "" {
		actorToken, ok := h.mintedTokens[actorRef]
		if !ok {
			return nil, fmt.Errorf("no minted token named %q (mint it with test_mint_token first)", actorRef)
		}
		form.Set("actor_token", actorToken)
		form.Set("actor_token_type", jwtTokenTypeURN)
	}
	if resource, _ := args["resource"].(string); resource != "" {
		form.Set("resource", resource)
	}

	baseURL := pkgoauth.NormalizeServerURL(h.currentInstance.Endpoint)
	status, body, err := postForm(ctx, baseURL+"/oauth/token", form)
	if err != nil {
		return nil, fmt.Errorf("token-exchange request failed: %w", err)
	}

	if status != http.StatusOK {
		oauthErr, oauthDesc := parseOAuthError(body)
		if h.debug {
			h.logger.Debug("🔐 Broker exchange rejected (status %d): %s - %s\n", status, oauthErr, oauthDesc)
		}
		return map[string]interface{}{
			"isError":           true,
			keySuccess:          false,
			"status":            status,
			"error":             oauthErr,
			"error_description": oauthDesc,
		}, nil
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access_token in token-exchange response")
	}

	claims, err := h.verifyAgainstMusterJWKS(ctx, baseURL, tokenResp.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("verifying minted token: %w", err)
	}

	if name, _ := args["name"].(string); name != "" {
		h.mintedTokens[name] = tokenResp.AccessToken
	}

	result := map[string]interface{}{
		keySuccess:     true,
		"claims":       claims,
		"access_token": tokenResp.AccessToken,
	}
	// Convenience top-level fields for contains assertions.
	if sub, ok := claims["sub"].(string); ok {
		result["subject"] = sub
	}
	if iss, ok := claims["iss"].(string); ok {
		result["issuer"] = iss
	}
	if act, ok := claims["act"].(map[string]interface{}); ok {
		if actSub, ok := act["sub"].(string); ok {
			result["actor_subject"] = actSub
		}
	}
	return result, nil
}

// handleCallProtectedMCP calls a tool on a protected mock MCP backend using a
// previously minted bearer token. A connection/auth failure (401) is returned as
// an error map so the wrong-audience reject scenario can assert on it.
// args: server, tool, token_ref (required); args (map) optional.
func (h *TestToolsHandler) handleCallProtectedMCP(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if h.currentInstance == nil {
		return nil, fmt.Errorf("current instance not available")
	}

	serverName, _ := args["server"].(string)
	if serverName == "" {
		return nil, fmt.Errorf("server argument is required")
	}
	toolName, _ := args["tool"].(string)
	if toolName == "" {
		return nil, fmt.Errorf("tool argument is required")
	}
	tokenRef, _ := args["token_ref"].(string)
	if tokenRef == "" {
		return nil, fmt.Errorf("token_ref argument is required")
	}
	token, ok := h.mintedTokens[tokenRef]
	if !ok {
		return nil, fmt.Errorf("no minted token named %q", tokenRef)
	}

	info, ok := h.currentInstance.MockHTTPServers[serverName]
	if !ok || info == nil {
		return nil, fmt.Errorf("mock MCP server %q not found", serverName)
	}

	toolArgs, _ := args["args"].(map[string]interface{})
	if toolArgs == nil {
		toolArgs = map[string]interface{}{}
	}

	client := NewMCPTestClientWithLogger(h.debug, h.logger)
	defer func() { _ = client.Close() }()

	if err := client.ConnectWithAuth(ctx, info.Endpoint, token); err != nil {
		return map[string]interface{}{
			"isError":  true,
			keySuccess: false,
			"error":    fmt.Sprintf("backend rejected token: %v", err),
		}, nil
	}

	result, err := client.CallToolDirect(ctx, toolName, toolArgs)
	if err != nil {
		return map[string]interface{}{
			"isError":  true,
			keySuccess: false,
			"error":    fmt.Sprintf("backend tool call failed: %v", err),
		}, nil
	}

	text := ""
	if result != nil && len(result.Content) > 0 {
		if tc, ok := mcp.AsTextContent(result.Content[0]); ok {
			text = tc.Text
		}
	}
	return map[string]interface{}{
		keySuccess: !result.IsError,
		"isError":  result.IsError,
		"response": text,
	}, nil
}

// verifyAgainstMusterJWKS fetches muster's JWKS, verifies the token signature and
// returns the decoded claims.
func (h *TestToolsHandler) verifyAgainstMusterJWKS(ctx context.Context, baseURL, token string) (map[string]interface{}, error) {
	parsed, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.ES256, jose.RS256})
	if err != nil {
		return nil, fmt.Errorf("parsing minted JWT: %w", err)
	}

	status, body, err := getURL(ctx, baseURL+"/.well-known/jwks.json")
	if err != nil {
		return nil, fmt.Errorf("fetching muster JWKS: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("fetching muster JWKS: status %d", status)
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("decoding muster JWKS: %w", err)
	}
	if len(set.Keys) == 0 {
		return nil, fmt.Errorf("muster JWKS is empty (is enableJWTMode set?)")
	}

	var payload []byte
	var verifyErr error
	for _, key := range set.Keys {
		if payload, verifyErr = parsed.Verify(key); verifyErr == nil {
			break
		}
	}
	if verifyErr != nil {
		return nil, fmt.Errorf("signature verification failed: %w", verifyErr)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("decoding minted claims: %w", err)
	}
	return claims, nil
}

func postForm(ctx context.Context, endpoint string, data url.Values) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, nil
}

func getURL(ctx context.Context, endpoint string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, nil
}

func parseOAuthError(body []byte) (string, string) {
	var e struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &e)
	if e.Error == "" {
		return strings.TrimSpace(string(body)), ""
	}
	return e.Error, e.ErrorDescription
}

func toStringSlice(v interface{}) []string {
	switch vv := v.(type) {
	case []string:
		return vv
	case []interface{}:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
