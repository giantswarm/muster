package testing

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/giantswarm/muster/internal/api"
)

// handleResolveAuthRedirect runs core_auth_login for a server and follows the
// muster-hosted start URL of the challenge one hop, the way a browser would,
// to see which authorization server the flow is sent to. args: server.
//
// The result names the mock OAuth server whose issuer the upstream
// authorization URL belongs to (authorization_server), plus the endpoint,
// client_id and scope on that URL, so a scenario can assert where a sign-in
// goes without knowing the mock servers' ports.
func (h *TestToolsHandler) handleResolveAuthRedirect(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	serverName, _ := args["server"].(string)
	if serverName == "" {
		return nil, fmt.Errorf("server argument is required")
	}
	if h.currentInstance == nil {
		return nil, fmt.Errorf("current instance not available")
	}

	startURL, err := h.callAuthenticateTool(ctx, serverName)
	if err != nil {
		return nil, fmt.Errorf("core_auth_login for %s did not return an auth URL: %w", serverName, err)
	}
	upstream := startURL
	if strings.Contains(startURL, "/oauth/proxy/start") {
		if upstream, err = h.resolveStartRedirect(ctx, startURL); err != nil {
			return nil, fmt.Errorf("failed to resolve start URL redirect: %w", err)
		}
	}
	parsed, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("upstream authorization URL %q: %w", upstream, err)
	}
	authHost := parsed.Scheme + "://" + parsed.Host
	authorizationServer := ""
	for name, info := range h.currentInstance.MockOAuthServers {
		if strings.HasPrefix(info.IssuerURL, authHost) {
			authorizationServer = name
			break
		}
	}
	return map[string]interface{}{
		api.FieldSuccess:         true,
		api.FieldServer:          serverName,
		"start_url":              startURL,
		"authorization_url":      upstream,
		"authorization_endpoint": authHost + parsed.Path,
		"authorization_server":   authorizationServer,
		"client_id":              parsed.Query().Get("client_id"),
		"scope":                  parsed.Query().Get("scope"),
	}, nil
}

// handlePinMCPServerAuthorizationServer rewrites spec.auth.authorizationServer
// of an MCPServer's filesystem definition: pins the issuer of the mock OAuth
// server named by issuer_ref (with optional scopes and grant_scope), adds the
// authorize/token endpoints of the mock server named by endpoints_ref, or
// removes the pin (clear: true). The filesystem detector then reconciles the
// change the way a CR update is reconciled. args: server, issuer_ref,
// endpoints_ref, scopes, grant_scope, clear.
func (h *TestToolsHandler) handlePinMCPServerAuthorizationServer(_ context.Context, args map[string]interface{}) (interface{}, error) {
	serverName, _ := args["server"].(string)
	if serverName == "" {
		return nil, fmt.Errorf("server argument is required")
	}
	if h.currentInstance == nil {
		return nil, fmt.Errorf("no muster instance available")
	}
	clear, _ := args["clear"].(bool)
	issuerRef, _ := args["issuer_ref"].(string)
	endpointsRef, _ := args["endpoints_ref"].(string)
	if !clear && issuerRef == "" {
		return nil, fmt.Errorf("issuer_ref is required unless clear is true")
	}

	filename := filepath.Join(h.currentInstance.ConfigPath, "muster", "mcpservers", serverName+".yaml")
	data, err := os.ReadFile(filename) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to read MCPServer definition %s: %w", filename, err)
	}
	var definition map[string]interface{}
	if err := yaml.Unmarshal(data, &definition); err != nil {
		return nil, fmt.Errorf("failed to parse MCPServer definition %s: %w", filename, err)
	}
	spec, _ := definition["spec"].(map[string]interface{})
	if spec == nil {
		return nil, fmt.Errorf("MCPServer definition %s has no spec", filename)
	}
	auth, _ := spec["auth"].(map[string]interface{})
	if auth == nil {
		auth = map[string]interface{}{"type": "oauth"}
	}

	var pin map[string]interface{}
	if clear {
		delete(auth, "authorizationServer")
	} else {
		issuer, ok := h.currentInstance.MockOAuthServers[issuerRef]
		if !ok {
			return nil, fmt.Errorf("issuer_ref names no mock OAuth server: %q", issuerRef)
		}
		pin = map[string]interface{}{"issuer": issuer.IssuerURL}
		if scopes, _ := args["scopes"].(string); scopes != "" {
			pin["scopes"] = scopes
		}
		if grantScope, _ := args["grant_scope"].(string); grantScope != "" {
			pin["grantScope"] = grantScope
		}
		if endpointsRef != "" {
			endpoints, ok := h.currentInstance.MockOAuthServers[endpointsRef]
			if !ok {
				return nil, fmt.Errorf("endpoints_ref names no mock OAuth server: %q", endpointsRef)
			}
			pin["authorizationEndpoint"] = endpoints.IssuerURL + "/authorize"
			pin["tokenEndpoint"] = endpoints.IssuerURL + "/token"
		}
		auth["authorizationServer"] = pin
	}
	spec["auth"] = auth
	definition["spec"] = spec

	out, err := yaml.Marshal(definition)
	if err != nil {
		return nil, fmt.Errorf("failed to render MCPServer definition %s: %w", filename, err)
	}
	if err := os.WriteFile(filename, out, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write MCPServer definition %s: %w", filename, err)
	}
	if h.debug {
		h.logger.Debug("Set spec.auth.authorizationServer of MCPServer '%s' to %v\n", serverName, pin)
	}
	return map[string]interface{}{
		api.FieldSuccess:      true,
		api.FieldServer:       serverName,
		"authorizationServer": pin,
		"cleared":             clear,
	}, nil
}
