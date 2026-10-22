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
	"github.com/giantswarm/muster/v5/internal/toolset"
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
	handler := actorBearerHandler(ab, mcpHandler, next, onAuthenticated, a.withPersonIDToken)
	for _, path := range actorBearerPaths {
		mux.Handle(path, handler)
	}
	logging.InfoWithAttrs("Aggregator", "Actor bearer enabled",
		slog.String("issuer", a.config.CredentialProvider.ActorBearer.Issuer),
		slog.String("audience", a.config.CredentialProvider.ActorBearer.Audience))
	return nil
}

// actorBearerHandler serves a request whose bearer is an actor JWT as the
// person the actor is bound to, and hands every other request to next. A
// refusal answers a generic reason; the detail is in muster's log.
//
// The actor JWT is never put in the request context as a forwardable token:
// muster's downstream servers never see it. withPersonToken, when set, puts
// the bound person's own login ID token there instead, for the servers that
// forward or exchange it.
func actorBearerHandler(ab *credprovider.ActorBearer, mcpHandler, next http.Handler, onAuthenticated func(context.Context, string), withPersonToken func(context.Context, *credprovider.ActorCaller) context.Context) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := bearerFromHeader(r.Header.Get("Authorization"))
		if bearer == "" || !ab.Claims(bearer) {
			next.ServeHTTP(w, r)
			return
		}
		caller, err := ab.Authenticate(r.Context(), bearer)
		switch {
		case errors.Is(err, credprovider.ErrBindingsUnavailable):
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		case errors.Is(err, credprovider.ErrActorNotAllowed):
			w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope"`)
			http.Error(w, "forbidden: the actor may not call muster", http.StatusForbidden)
			return
		case err != nil:
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			http.Error(w, "unauthorized: invalid actor token", http.StatusUnauthorized)
			return
		}

		ctx := api.WithSubject(r.Context(), caller.Subject)
		ctx = api.WithSessionID(ctx, caller.SessionID)
		ctx = api.WithAgentCaller(ctx)
		// The binding's toolset bounds the request whatever header it sends:
		// the header can narrow it and never widen it.
		ctx = toolset.ContextWithBound(ctx, caller.Toolset)
		if withPersonToken != nil {
			ctx = withPersonToken(ctx, caller)
		}
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
