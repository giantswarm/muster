package translator

import (
	"fmt"
	"net/url"
	"strconv"

	v1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// Recognized MCPServer spec types.
const (
	specTypeStdio          = "stdio"
	specTypeStreamableHTTP = "streamable-http"
	specTypeSSE            = "sse"
)

const shimPath = "/mcp"

// Transform produces a Model for the given MCPServer name and spec. It is pure:
// the result depends only on the inputs and never performs I/O.
//
// For spec.Type == "stdio" the resulting Model carries a ShimRequest; the
// reconciler is expected to resolve the shim's listener Endpoint and inject
// Host and Port into the matching Backend before handing the Model to a
// ConfigEmitter.
func Transform(name string, spec v1alpha1.MCPServerSpec) (Model, error) {
	if name == "" {
		return Model{}, fmt.Errorf("translator: MCPServer name is required")
	}

	authn, err := transformAuth(spec.Auth)
	if err != nil {
		return Model{}, fmt.Errorf("translator: auth: %w", err)
	}

	backend, shim, err := transformBackend(name, spec)
	if err != nil {
		return Model{}, fmt.Errorf("translator: backend: %w", err)
	}

	route := Route{
		Name:       name,
		PathMatch:  shimPath + "/" + name,
		BackendRef: name,
		PolicyRef:  name,
	}
	policy := Policy{Name: name, Authn: authn}

	model := Model{
		Backends: []Backend{backend},
		Routes:   []Route{route},
		Policies: []Policy{policy},
	}
	if shim != nil {
		model.Shims = []ShimRequest{*shim}
	}
	return model, nil
}

func transformBackend(name string, spec v1alpha1.MCPServerSpec) (Backend, *ShimRequest, error) {
	switch spec.Type {
	case specTypeStdio:
		if spec.Command == "" {
			return Backend{}, nil, fmt.Errorf("stdio MCPServer requires spec.command")
		}
		backend := Backend{
			Name:     name,
			Path:     shimPath,
			Protocol: ProtocolStreamableHTTP,
		}
		shim := &ShimRequest{
			Name:    name,
			Command: spec.Command,
			Args:    spec.Args,
			Env:     spec.Env,
		}
		return backend, shim, nil

	case specTypeStreamableHTTP:
		backend, err := backendFromURL(name, spec.URL, ProtocolStreamableHTTP)
		if err != nil {
			return Backend{}, nil, err
		}
		return backend, nil, nil

	case specTypeSSE:
		backend, err := backendFromURL(name, spec.URL, ProtocolSSE)
		if err != nil {
			return Backend{}, nil, err
		}
		return backend, nil, nil

	default:
		return Backend{}, nil, fmt.Errorf("unsupported spec.type %q", spec.Type)
	}
}

func backendFromURL(name, raw string, protocol Protocol) (Backend, error) {
	if raw == "" {
		return Backend{}, fmt.Errorf("spec.url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return Backend{}, fmt.Errorf("parse spec.url %q: %w", raw, err)
	}
	host := parsed.Hostname()
	if host == "" {
		return Backend{}, fmt.Errorf("spec.url %q has no host", raw)
	}
	var port int
	if p := parsed.Port(); p != "" {
		// net/url already rejected non-numeric ports at Parse.
		port, _ = strconv.Atoi(p)
	} else {
		switch parsed.Scheme {
		case "http":
			port = 80
		case "https":
			port = 443
		default:
			return Backend{}, fmt.Errorf("unsupported url scheme %q", parsed.Scheme)
		}
	}
	return Backend{
		Name:     name,
		Host:     host,
		Port:     port,
		Path:     parsed.Path,
		Protocol: protocol,
	}, nil
}

func transformAuth(auth *v1alpha1.MCPServerAuth) (AuthnConfig, error) {
	if auth == nil {
		return AuthnConfig{Type: AuthnTypeNone}, nil
	}

	typ, err := parseAuthnType(auth.Type)
	if err != nil {
		return AuthnConfig{}, err
	}

	if auth.AuthorizationServer != nil {
		if typ != AuthnTypeOAuth {
			return AuthnConfig{}, fmt.Errorf("authorizationServer is only valid when auth.type is oauth")
		}
		if auth.ForwardToken {
			return AuthnConfig{}, fmt.Errorf("forwardToken and authorizationServer are mutually exclusive")
		}
		if auth.TokenExchange != nil && auth.TokenExchange.Enabled {
			return AuthnConfig{}, fmt.Errorf("tokenExchange.enabled and authorizationServer are mutually exclusive")
		}
	}

	cfg := AuthnConfig{
		Type:              typ,
		ForwardToken:      auth.ForwardToken,
		RequiredAudiences: auth.RequiredAudiences,
	}
	if auth.TokenExchange != nil {
		cfg.TokenExchange = tokenExchangeFromSpec(auth.TokenExchange)
	}
	if auth.Teleport != nil {
		cfg.Teleport = teleportFromSpec(auth.Teleport)
	}
	if auth.AuthorizationServer != nil {
		cfg.AuthorizationServer = &AuthorizationServer{
			Issuer: auth.AuthorizationServer.Issuer.Normalize(),
			Scopes: auth.AuthorizationServer.Scopes,
		}
	}
	return cfg, nil
}

func parseAuthnType(s string) (AuthnType, error) {
	switch s {
	case "", string(AuthnTypeNone):
		return AuthnTypeNone, nil
	case string(AuthnTypeOAuth):
		return AuthnTypeOAuth, nil
	case string(AuthnTypeTeleport):
		return AuthnTypeTeleport, nil
	default:
		return "", fmt.Errorf("unsupported auth.type %q", s)
	}
}

func tokenExchangeFromSpec(tx *v1alpha1.TokenExchangeConfig) *TokenExchangeAuthn {
	out := &TokenExchangeAuthn{
		Enabled:          tx.Enabled,
		DexTokenEndpoint: tx.DexTokenEndpoint,
		ExpectedIssuer:   tx.ExpectedIssuer,
		ConnectorID:      tx.ConnectorID,
		Scopes:           tx.Scopes,
	}
	if tx.ClientCredentialsSecretRef != nil {
		out.ClientCredentialsSecretName = tx.ClientCredentialsSecretRef.Name
		out.ClientCredentialsSecretNamespace = tx.ClientCredentialsSecretRef.Namespace
	}
	return out
}

func teleportFromSpec(tp *v1alpha1.TeleportAuthConfig) *TeleportAuthn {
	return &TeleportAuthn{
		IdentitySecretName:      tp.IdentitySecretName,
		IdentitySecretNamespace: tp.IdentitySecretNamespace,
		IdentityDir:             tp.IdentityDir,
		AppName:                 tp.AppName,
	}
}
