package api

import "time"

// LoginTokenMirror is implemented by an OAuthHandler that keeps the ID token
// of the person's login at muster -- the credential SSO token forwarding hands
// to backends that trust muster's IdP -- apart from the per-server grants the
// OAuth proxy obtains. Both can come from the same issuer: an MCPServer may
// pin muster's own IdP as its authorization server. Filed under one key, the
// mirror of the login token, written on every request that carries an
// externally issued bearer, overwrote the grant and the server's tools failed
// with "no valid token available" right after a successful sign-in
// (giantswarm/muster#1174).
type LoginTokenMirror interface {
	// StoreLoginIDToken records the session's login ID token under the
	// issuer without touching any grant from that issuer.
	StoreLoginIDToken(sessionID, userID, issuer, idToken string, expiresAt time.Time)

	// LoginIDToken returns the session's login ID token for the issuer, or
	// nil when none is recorded or it expired.
	LoginIDToken(sessionID, issuer string) *OAuthToken
}

// StoreLoginIDToken files the login ID token through the handler's mirror
// when it keeps one, and as the session's plain token for the issuer
// otherwise.
func StoreLoginIDToken(h OAuthHandler, sessionID, userID, issuer, idToken string, expiresAt time.Time) {
	if h == nil {
		return
	}
	if mirror, ok := h.(LoginTokenMirror); ok {
		mirror.StoreLoginIDToken(sessionID, userID, issuer, idToken, expiresAt)
		return
	}
	h.StoreToken(sessionID, userID, issuer, &OAuthToken{IDToken: idToken, ExpiresAt: expiresAt})
}

// LoginIDToken reads the login ID token through the handler's mirror when it
// keeps one, falling back to the session's token for the issuer: the entry an
// earlier muster wrote, still in the store until the session's next request
// or login rewrites it.
func LoginIDToken(h OAuthHandler, sessionID, issuer string) *OAuthToken {
	if h == nil {
		return nil
	}
	if mirror, ok := h.(LoginTokenMirror); ok {
		if token := mirror.LoginIDToken(sessionID, issuer); token != nil && token.IDToken != "" {
			return token
		}
	}
	return h.GetFullTokenByIssuer(sessionID, issuer)
}

// PinnedLoginMirror is implemented by a LoginTokenMirror that records which
// session filed each login ID token. A Session binding pins its actor to the
// login of the session its bind request came from: the bearer of that request
// is a login ID token muster filed for that session, either one muster obtained
// at its own sign-in (a login family) or one a client forwarded to muster.
type PinnedLoginMirror interface {
	// LoginSessionForIDToken returns the session that filed idToken as its
	// login ID token, or "" when none did. A login family is preferred over a
	// forwarded session that filed the same token.
	LoginSessionForIDToken(idToken string) string

	// ClearLoginIDToken removes the session's login ID token for the issuer.
	ClearLoginIDToken(sessionID, issuer string)
}

// LoginSessionForIDToken returns, through the handler's mirror, the session
// that filed idToken as its login ID token, or "".
func LoginSessionForIDToken(h OAuthHandler, idToken string) string {
	mirror, ok := h.(PinnedLoginMirror)
	if !ok || idToken == "" {
		return ""
	}
	return mirror.LoginSessionForIDToken(idToken)
}

// SessionLoginIDToken returns the session's own live login ID token for the
// issuer through the handler's mirror, or nil. Unlike LoginIDToken it never
// falls back to the session's plain token for the issuer, which can be a grant
// minted for another client.
func SessionLoginIDToken(h OAuthHandler, sessionID, issuer string) *OAuthToken {
	mirror, ok := h.(LoginTokenMirror)
	if !ok || sessionID == "" {
		return nil
	}
	if token := mirror.LoginIDToken(sessionID, issuer); token != nil && token.IDToken != "" {
		return token
	}
	return nil
}

// ClearLoginIDToken removes the session's login ID token through the
// handler's mirror, when it keeps one.
func ClearLoginIDToken(h OAuthHandler, sessionID, issuer string) {
	if mirror, ok := h.(PinnedLoginMirror); ok {
		mirror.ClearLoginIDToken(sessionID, issuer)
	}
}
