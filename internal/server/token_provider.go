package server

import (
	"context"

	mcpoauth "github.com/giantswarm/mcp-oauth"
	"github.com/giantswarm/mcp-oauth/providers"
	"golang.org/x/oauth2"
)

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

const (
	// accessTokenKey is the context key for storing the user's OAuth access token.
	// This token can be used for downstream API authentication.
	//nolint:gosec // G101 false positive - this is a context key name, not a credential
	accessTokenKey contextKey = "oauth_access_token"
)

// UserInfo represents user information from an OAuth provider.
// This is a type alias for the library's providers.UserInfo type.
type UserInfo = providers.UserInfo

// ContextWithAccessToken creates a context with the given OAuth ID token.
// This is used to pass the user's OAuth ID token for downstream
// authentication (e.g., to remote MCP servers).
func ContextWithAccessToken(ctx context.Context, idToken string) context.Context {
	return context.WithValue(ctx, accessTokenKey, idToken)
}

// GetAccessTokenFromContext retrieves the OAuth ID token from the context.
// Returns the ID token and true if present, or empty string and false if not available.
func GetAccessTokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(accessTokenKey).(string)
	return token, ok && token != ""
}

// UserInfoFromContext retrieves the authenticated user's info from the context.
// This is a wrapper around the mcp-oauth library's UserInfoFromContext function.
// The user info is set by the OAuth ValidateToken middleware after successful
// JWT validation.
//
// Returns the UserInfo pointer and true if present, or nil and false if not available.
func UserInfoFromContext(ctx context.Context) (*UserInfo, bool) {
	return mcpoauth.UserInfoFromContext(ctx)
}

// HasUserInfo checks if the context contains authenticated user information.
func HasUserInfo(ctx context.Context) bool {
	user, ok := UserInfoFromContext(ctx)
	return ok && user != nil
}

// GetUserEmailFromContext extracts just the email address from the context.
// Returns empty string if no user info is available.
func GetUserEmailFromContext(ctx context.Context) string {
	user, ok := UserInfoFromContext(ctx)
	if !ok || user == nil {
		return ""
	}
	return user.Email
}

// GetUserGroupsFromContext extracts the user's group memberships from the context.
// Returns nil if no user info is available.
func GetUserGroupsFromContext(ctx context.Context) []string {
	user, ok := UserInfoFromContext(ctx)
	if !ok || user == nil {
		return nil
	}
	return user.Groups
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
