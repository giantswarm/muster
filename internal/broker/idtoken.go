package broker

import "golang.org/x/oauth2"

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
