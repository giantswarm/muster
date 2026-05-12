package broker

import "golang.org/x/oauth2"

// GetIDToken returns the OIDC id_token carried in the OAuth2 token's Extra
// data, or "" when absent or not a string.
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
