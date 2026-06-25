package server

import (
	"context"

	"github.com/giantswarm/mcp-oauth/providers"
	"golang.org/x/oauth2"
)

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

// idTokenKey is the context key for storing the user's OIDC ID token.
// This token can be used for downstream API authentication.
//
//nolint:gosec // G101 false positive - this is a context key name, not a credential
const idTokenKey contextKey = "oauth_id_token"

// bearerTokenKey is the context key for the raw inbound Authorization bearer
// token. localMint uses it as the RFC 8693 subject token.
//
//nolint:gosec // G101 false positive - this is a context key name, not a credential
const bearerTokenKey contextKey = "oauth_bearer_token"

// actorTokenKey is the context key for the raw inbound actor token (HeaderActorToken).
// localMint uses it as the RFC 8693 actor token on the delegation path.
//
//nolint:gosec // G101 false positive - this is a context key name, not a credential
const actorTokenKey contextKey = "oauth_actor_token"

// HeaderActorToken carries the agent's workload token alongside the user's
// Authorization bearer on a localMint call_tool request. The value is a
// "Bearer <token>" string, mirroring Authorization. This is a contract shared
// with the kagent token provider that sends it.
//
//nolint:gosec // G101 false positive - this is an HTTP header name, not a credential
const HeaderActorToken = "X-Actor-Token"

// UserInfo represents user information from an OAuth provider.
// This is a type alias for the library's providers.UserInfo type.
type UserInfo = providers.UserInfo

// ContextWithIDToken creates a context with the given OIDC ID token.
// This is used to pass the user's ID token for downstream
// authentication (e.g., to remote MCP servers).
func ContextWithIDToken(ctx context.Context, idToken string) context.Context {
	return context.WithValue(ctx, idTokenKey, idToken)
}

// GetIDTokenFromContext retrieves the OIDC ID token from the context.
// Returns the ID token and true if present, or empty string and false if not available.
func GetIDTokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(idTokenKey).(string)
	return token, ok && token != ""
}

// ContextWithBearerToken stores the raw inbound bearer token (subject) for localMint.
func ContextWithBearerToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, bearerTokenKey, token)
}

// GetBearerTokenFromContext returns the raw inbound bearer token, or "" when absent.
func GetBearerTokenFromContext(ctx context.Context) string {
	token, _ := ctx.Value(bearerTokenKey).(string)
	return token
}

// ContextWithActorToken stores the raw inbound actor token (scheme stripped) for localMint.
func ContextWithActorToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, actorTokenKey, token)
}

// GetActorTokenFromContext returns the raw inbound actor token, or "" when absent.
func GetActorTokenFromContext(ctx context.Context) string {
	token, _ := ctx.Value(actorTokenKey).(string)
	return token
}

// CallerTokens is the set of per-request credential tokens (subject bearer,
// ID token, actor) carried as a unit so a context rebuilt off the request path
// cannot silently drop one.
type CallerTokens struct {
	IDToken string
	Bearer  string
	Actor   string
}

// CallerTokensFromContext snapshots the credential tokens present on ctx.
func CallerTokensFromContext(ctx context.Context) CallerTokens {
	idToken, _ := GetIDTokenFromContext(ctx)
	return CallerTokens{
		IDToken: idToken,
		Bearer:  GetBearerTokenFromContext(ctx),
		Actor:   GetActorTokenFromContext(ctx),
	}
}

// ContextWithCallerTokens stores all three tokens on ctx via their dedicated
// keys so the per-field getters observe identical values.
func ContextWithCallerTokens(ctx context.Context, tokens CallerTokens) context.Context {
	ctx = ContextWithIDToken(ctx, tokens.IDToken)
	ctx = ContextWithBearerToken(ctx, tokens.Bearer)
	ctx = ContextWithActorToken(ctx, tokens.Actor)
	return ctx
}

// GetIDToken extracts the ID token from an OAuth2 token.
// OIDC providers include an id_token in the Extra data.
// Kubernetes OIDC authentication requires the ID token, not the access token.
func GetIDToken(token *oauth2.Token) string {
	if token == nil {
		return ""
	}
	idToken := token.Extra("id_token")
	if idToken == nil {
		return ""
	}
	if s, ok := idToken.(string); ok {
		return s
	}
	return ""
}
