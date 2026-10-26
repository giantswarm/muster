package api

import (
	"context"
	"sync"
)

// BrowserIdentity signs the browser in to muster's identity provider to learn
// who is in it, granting nothing. The OAuth proxy start endpoint uses it to
// make sure the person completing a sign-in to a server is the person the
// sign-in was started for (MCP URL mode elicitation: the server must bind
// the elicitation to the user who completes it).
//
// It is registered when muster runs its own OAuth server. Without one no
// session has a person to compare against, and no check is made.
type BrowserIdentity interface {
	// SignInURL returns the provider URL the browser is sent to. The browser
	// comes back to the OAuth proxy start endpoint with code and state.
	// codeChallenge is the S256 challenge of the verifier Subject is given.
	SignInURL(ctx context.Context, state, codeChallenge string) (string, error)

	// Subject redeems the code the start endpoint received and returns the
	// subject of the person who signed in, the subject sessions are keyed by.
	Subject(ctx context.Context, code, codeVerifier string) (string, error)
}

var (
	browserIdentity      BrowserIdentity
	browserIdentityMutex sync.RWMutex
)

// RegisterBrowserIdentity registers the browser identity check.
func RegisterBrowserIdentity(b BrowserIdentity) {
	browserIdentityMutex.Lock()
	defer browserIdentityMutex.Unlock()
	browserIdentity = b
}

// GetBrowserIdentity returns the registered browser identity check, or nil
// when muster runs no OAuth server.
func GetBrowserIdentity() BrowserIdentity {
	browserIdentityMutex.RLock()
	defer browserIdentityMutex.RUnlock()
	return browserIdentity
}
