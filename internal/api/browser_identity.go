package api

import (
	"context"
	"net/url"
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
	// comes back to returnURL, on the issuer's origin, with code and state.
	// codeChallenge is the S256 challenge of the verifier Subject is given.
	SignInURL(ctx context.Context, returnURL *url.URL, state, codeChallenge string) (string, error)

	// Identity redeems the code returnURL received and returns who signed in.
	Identity(ctx context.Context, returnURL *url.URL, code, codeVerifier string) (SignInIdentity, error)
}

// SignInIdentity is the person a request was authenticated as at an identity
// provider: the issuer and its raw sub claim, together the only stable
// identifier of a person (OIDC Core §5.7). It differs from the session
// subject when a trusted issuer maps the subject from another claim.
type SignInIdentity struct {
	Issuer  string
	Subject string
}

type signInIdentityContextKey struct{}

// WithSignInIdentity returns a context carrying the request's sign-in identity.
func WithSignInIdentity(ctx context.Context, identity SignInIdentity) context.Context {
	return context.WithValue(ctx, signInIdentityContextKey{}, identity)
}

// SignInIdentityFromContext returns the request's sign-in identity; the zero
// value when the request carries none.
func SignInIdentityFromContext(ctx context.Context) SignInIdentity {
	identity, _ := ctx.Value(signInIdentityContextKey{}).(SignInIdentity)
	return identity
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
