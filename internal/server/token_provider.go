package server

import (
	"context"

	"golang.org/x/oauth2"
)

type contextKey string

//nolint:gosec // G101 false positive - this is a context key name, not a credential
const idTokenKey contextKey = "oauth_id_token"

// ContextWithIDToken returns ctx carrying the OIDC ID token for downstream
// authentication (remote MCP servers, Kubernetes audiences).
func ContextWithIDToken(ctx context.Context, idToken string) context.Context {
	return context.WithValue(ctx, idTokenKey, idToken)
}

// GetIDTokenFromContext retrieves the OIDC ID token from the context.
// Returns the ID token and true if present, or empty string and false if not available.
func GetIDTokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(idTokenKey).(string)
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
