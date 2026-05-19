package agentgateway

import (
	"fmt"
	"net/url"
	"strconv"

	v1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

const (
	specTypeStdio          = "stdio"
	specTypeStreamableHTTP = "streamable-http"
	specTypeSSE            = "sse"
)

const routePathPrefix = "/mcp"

// NewConfig produces an agentgateway Config for the given MCPServer name,
// namespace, and spec. Same input always yields the same output and NewConfig
// performs no I/O.
func NewConfig(name, namespace string, spec v1alpha1.MCPServerSpec) (Config, error) {
	if name == "" {
		return Config{}, fmt.Errorf("agentgateway: MCPServer name is required")
	}
	if namespace == "" {
		return Config{}, fmt.Errorf("agentgateway: MCPServer namespace is required")
	}

	authn, err := authFromSpec(spec.Auth)
	if err != nil {
		return Config{}, fmt.Errorf("agentgateway: auth: %w", err)
	}

	backend, err := backendFromSpec(name, spec)
	if err != nil {
		return Config{}, fmt.Errorf("agentgateway: backend: %w", err)
	}

	route := Route{
		Name:       name,
		PathMatch:  routePathPrefix + "/" + name,
		BackendRef: name,
		PolicyRef:  name,
	}
	policy := Policy{Name: name, Authn: authn}

	return Config{
		Name:      name,
		Namespace: namespace,
		Backends:  []Backend{backend},
		Routes:    []Route{route},
		Policies:  []Policy{policy},
	}, nil
}

func backendFromSpec(name string, spec v1alpha1.MCPServerSpec) (Backend, error) {
	switch spec.Type {
	case specTypeStdio:
		if spec.Command == "" {
			return Backend{}, fmt.Errorf("stdio MCPServer requires spec.command")
		}
		return Backend{
			Name: name,
			Target: StdioTarget{
				Command: spec.Command,
				Args:    spec.Args,
				Env:     spec.Env,
			},
		}, nil

	case specTypeStreamableHTTP:
		target, err := httpTargetFromURL(spec.URL, StreamableHTTP)
		if err != nil {
			return Backend{}, err
		}
		return Backend{Name: name, Target: target}, nil

	case specTypeSSE:
		target, err := httpTargetFromURL(spec.URL, SSE)
		if err != nil {
			return Backend{}, err
		}
		return Backend{Name: name, Target: target}, nil

	default:
		return Backend{}, fmt.Errorf("unsupported spec.type %q", spec.Type)
	}
}

func httpTargetFromURL(raw string, protocol HTTPProtocol) (HTTPTarget, error) {
	if raw == "" {
		return HTTPTarget{}, fmt.Errorf("spec.url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return HTTPTarget{}, fmt.Errorf("parse spec.url %q: %w", raw, err)
	}
	host := parsed.Hostname()
	if host == "" {
		return HTTPTarget{}, fmt.Errorf("spec.url %q has no host", raw)
	}
	var port int
	if p := parsed.Port(); p != "" {
		parsedPort, err := strconv.Atoi(p)
		if err != nil {
			return HTTPTarget{}, fmt.Errorf("spec.url %q has non-numeric port %q: %w", raw, p, err)
		}
		port = parsedPort
	} else {
		switch parsed.Scheme {
		case "http":
			port = 80
		case "https":
			port = 443
		default:
			return HTTPTarget{}, fmt.Errorf("unsupported url scheme %q", parsed.Scheme)
		}
	}
	return HTTPTarget{Protocol: protocol, Host: host, Port: port, Path: parsed.Path}, nil
}

func authFromSpec(auth *v1alpha1.MCPServerAuth) (Authn, error) {
	if auth == nil {
		return Authn{Type: AuthnTypeNone}, nil
	}

	typ, err := parseAuthnType(auth.Type)
	if err != nil {
		return Authn{}, err
	}

	if auth.AuthorizationServer != nil {
		if typ != AuthnTypeOAuth {
			return Authn{}, fmt.Errorf("authorizationServer is only valid when auth.type is oauth")
		}
		if auth.ForwardToken {
			return Authn{}, fmt.Errorf("forwardToken and authorizationServer are mutually exclusive")
		}
		if auth.TokenExchange != nil && auth.TokenExchange.Enabled {
			return Authn{}, fmt.Errorf("tokenExchange.enabled and authorizationServer are mutually exclusive")
		}
	}

	out := Authn{
		Type:              typ,
		ForwardToken:      auth.ForwardToken,
		RequiredAudiences: auth.RequiredAudiences,
	}
	if auth.TokenExchange != nil {
		out.TokenExchange = tokenExchangeFromSpec(auth.TokenExchange)
	}
	if auth.AuthorizationServer != nil {
		out.AuthorizationServer = &AuthorizationServer{
			Issuer: auth.AuthorizationServer.Issuer.Normalize(),
			Scopes: auth.AuthorizationServer.Scopes,
		}
	}
	return out, nil
}

func parseAuthnType(s string) (AuthnType, error) {
	switch s {
	case "", string(AuthnTypeNone):
		return AuthnTypeNone, nil
	case string(AuthnTypeOAuth):
		return AuthnTypeOAuth, nil
	default:
		return "", fmt.Errorf("unsupported auth.type %q", s)
	}
}

func tokenExchangeFromSpec(tokenExchange *v1alpha1.TokenExchangeConfig) *TokenExchange {
	out := &TokenExchange{
		Enabled:          tokenExchange.Enabled,
		DexTokenEndpoint: tokenExchange.DexTokenEndpoint,
		ExpectedIssuer:   tokenExchange.ExpectedIssuer,
		ConnectorID:      tokenExchange.ConnectorID,
		Scopes:           tokenExchange.Scopes,
	}
	if tokenExchange.ClientCredentialsSecretRef != nil {
		out.ClientCredentialsSecretName = tokenExchange.ClientCredentialsSecretRef.Name
		out.ClientCredentialsSecretNamespace = tokenExchange.ClientCredentialsSecretRef.Namespace
	}
	return out
}
