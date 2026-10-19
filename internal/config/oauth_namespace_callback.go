package config

import (
	"fmt"
	"strings"
)

const (
	// OAuthProxyNamespaceCallbackPrefix starts the OAuth proxy callback path
	// of a namespace other than muster's: a server outside muster's namespace
	// signs in through <publicURL>/oauth/ns/<namespace>/callback, never
	// through the plain callback.
	OAuthProxyNamespaceCallbackPrefix = "/oauth/ns/"

	// OAuthProxyNamespaceCallbackPattern is the ServeMux pattern of the
	// namespace callbacks.
	OAuthProxyNamespaceCallbackPattern = OAuthProxyNamespaceCallbackPrefix + "{namespace}/callback"
)

// OAuthProxyNamespaceCallbackPath returns the callback path of a namespace
// other than muster's.
func OAuthProxyNamespaceCallbackPath(namespace string) string {
	return OAuthProxyNamespaceCallbackPrefix + namespace + "/callback"
}

// ValidateNamespaceCallback refuses a callback path that is a prefix of the
// namespace callbacks, or that starts with their prefix. An authorization
// server that matches a redirect URI by prefix would otherwise accept a
// namespace's redirect URI for a client of muster's namespace, or the reverse.
func (c *OAuthMCPClientConfig) ValidateNamespaceCallback() error {
	callbackPath := c.CallbackPath
	if callbackPath == "" {
		callbackPath = DefaultOAuthProxyCallbackPath
	}
	if strings.HasPrefix(OAuthProxyNamespaceCallbackPrefix, callbackPath) ||
		strings.HasPrefix(callbackPath, OAuthProxyNamespaceCallbackPrefix) {
		return fmt.Errorf("oauth.mcpClient.callbackPath %q overlaps the namespace callbacks %s<namespace>/callback; choose a path that is not a prefix of them and does not start with them",
			callbackPath, OAuthProxyNamespaceCallbackPrefix)
	}
	return nil
}
