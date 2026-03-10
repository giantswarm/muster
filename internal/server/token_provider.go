package server

import (
	"context"

	"github.com/giantswarm/mcp-oauth/providers"
	"golang.org/x/oauth2"
)

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

const (
	// idTokenKey is the context key for storing the user's OIDC ID token.
	// This token can be used for downstream API authentication.
	//nolint:gosec // G101 false positive - this is a context key name, not a credential
	idTokenKey contextKey = "oauth_id_token"

	// upstreamAccessTokenKey is the context key for storing the upstream IdP's access token.
	// This is used to detect token refresh (the access token changes on refresh,
	// even when the ID token is preserved).
	//nolint:gosec // G101 false positive - this is a context key name, not a credential
	upstreamAccessTokenKey contextKey = "oauth_upstream_access_token"
)

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

// ContextWithUpstreamAccessToken creates a context with the upstream IdP's access token.
// This is used for detecting token refresh - the access token changes on refresh,
// even when the ID token is preserved. By tracking the access token, we can
// detect both re-authentication (new ID token) and token refresh (new access token).
func ContextWithUpstreamAccessToken(ctx context.Context, accessToken string) context.Context {
	return context.WithValue(ctx, upstreamAccessTokenKey, accessToken)
}

// GetUpstreamAccessTokenFromContext retrieves the upstream IdP's access token from context.
// Returns the access token and true if present, or empty string and false if not available.
func GetUpstreamAccessTokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(upstreamAccessTokenKey).(string)
	return token, ok && token != ""
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
