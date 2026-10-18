package aggregator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/credprovider"
	"github.com/giantswarm/muster/v5/pkg/logging"
	"github.com/giantswarm/muster/v5/pkg/tlsutil"
)

// actorBearerPaths are the MCP endpoints an actor JWT may call.
var actorBearerPaths = []string{"/mcp", "/sse", "/message"}

// newActorBearer builds the actor-JWT authenticator on the credential
// provider's binding store. Returns nil when the actor bearer is disabled.
func (a *AggregatorServer) newActorBearer() (*credprovider.ActorBearer, error) {
	cfg := a.config.CredentialProvider.ActorBearer
	if !cfg.Enabled {
		return nil, nil
	}
	if a.credProvider == nil {
		return nil, fmt.Errorf("actor bearer: needs the credential provider (credentialProvider.enabled) for its Session bindings")
	}
	opts := credprovider.OIDCVerifierOptions{
		Issuer:              cfg.Issuer,
		JwksURL:             cfg.JwksURL,
		Audience:            cfg.Audience,
		AllowPrivateIPHosts: cfg.AllowPrivateIPJWKSHosts,
	}
	if cfg.CAFile != "" {
		pool, err := tlsutil.LoadCAPool(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("actor bearer: %w", err)
		}
		opts.RootCAs = pool
	}
	verify, err := credprovider.NewOIDCActorTokenVerifier(opts)
	if err != nil {
		return nil, err
	}
	return credprovider.NewActorBearer(credprovider.ActorBearerOptions{
		Issuer:     cfg.Issuer,
		Verify:     verify,
		Store:      a.credProvider.Store(),
		BindingTTL: a.credProvider.BindingTTL(),
	})
}

// registerActorBearerRoutes puts the actor-JWT authenticator in front of the
// MCP endpoints: a request whose bearer names the actor-JWT issuer is served
// as the bound person, every other request goes to next unchanged. A
// disabled actor bearer registers nothing.
func (a *AggregatorServer) registerActorBearerRoutes(mux *http.ServeMux, mcpHandler, next http.Handler, onAuthenticated func(context.Context, string)) error {
	ab, err := a.newActorBearer()
	if err != nil || ab == nil {
		return err
	}
	handler := actorBearerHandler(ab, mcpHandler, next, onAuthenticated)
	for _, path := range actorBearerPaths {
		mux.Handle(path, handler)
	}
	logging.InfoWithAttrs("Aggregator", "Actor bearer enabled",
		slog.String("issuer", a.config.CredentialProvider.ActorBearer.Issuer),
		slog.String("audience", a.config.CredentialProvider.ActorBearer.Audience))
	return nil
}

// actorBearerHandler serves a request whose bearer is an actor JWT as the
// person the actor is bound to, and hands every other request to next.
//
// The actor JWT is never put in the request context as a forwardable token:
// muster's downstream servers never see it.
func actorBearerHandler(ab *credprovider.ActorBearer, mcpHandler, next http.Handler, onAuthenticated func(context.Context, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := bearerFromHeader(r.Header.Get("Authorization"))
		if bearer == "" || !ab.Claims(bearer) {
			next.ServeHTTP(w, r)
			return
		}
		caller, err := ab.Authenticate(r.Context(), bearer)
		switch {
		case errors.Is(err, credprovider.ErrBindingsUnavailable):
			http.Error(w, "temporarily unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		case errors.Is(err, credprovider.ErrActorNotAllowed):
			w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope"`)
			http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
			return
		case err != nil:
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}

		ctx := api.WithSubject(r.Context(), caller.Subject)
		ctx = api.WithSessionID(ctx, caller.SessionID)
		logging.DebugWithAttrs("Aggregator", "Actor bearer accepted",
			slog.String("sub", caller.Subject),
			slog.String("session", caller.Session),
			slog.String("actor", caller.Actor),
			slog.Time("expiresAt", caller.Expiry))
		if onAuthenticated != nil {
			onAuthenticated(ctx, caller.SessionID)
		}
		mcpHandler.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerFromHeader returns the token of a "Bearer <token>" Authorization
// value, or "".
func bearerFromHeader(value string) string {
	const prefix = "Bearer "
	if len(value) > len(prefix) && strings.EqualFold(value[:len(prefix)], prefix) {
		return value[len(prefix):]
	}
	return ""
}
