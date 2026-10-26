package oauth

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

const (
	// subjectGrantRefreshMargin is the lead time before a subject-scoped
	// grant's access token expires at which a lookup refreshes it. A relying
	// party keeps a released token for a while without asking again: the
	// egress gateway caches a credential provider's answer for 300 s per
	// actor and URI, the Dev Portal's broker cache re-asks 240 s before
	// expiry. Fifteen minutes is well beyond both, so a token handed out is
	// never used past its expiry.
	subjectGrantRefreshMargin = 15 * time.Minute

	// subjectGrantRefreshTimeout bounds one refresh round trip. The refresh
	// runs on a context detached from the caller's cancellation because
	// concurrent callers share it (singleflight): the first caller giving
	// up must not fail the others.
	subjectGrantRefreshTimeout = 30 * time.Second
)

// refreshMargin returns how long before ExpiresAt a token counts as due for
// refresh: subjectGrantRefreshMargin, capped at half the token's lifetime so
// a short-lived token is not refreshed on every lookup, and never below the
// margin at which the store hides it.
func refreshMargin(token *pkgoauth.Token) time.Duration {
	margin := subjectGrantRefreshMargin
	if token.ExpiresIn > 0 {
		if half := time.Duration(token.ExpiresIn) * time.Second / 2; half < margin {
			margin = half
		}
	}
	if margin < tokenExpiryMargin {
		margin = tokenExpiryMargin
	}
	return margin
}

// refreshDue reports whether a token has expired or will within its refresh
// margin. A token without expiry is never due.
func refreshDue(token *pkgoauth.Token) bool {
	if token == nil || token.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(refreshMargin(token)).After(token.ExpiresAt)
}

// usableToken returns the token when its access token is still accepted (not
// expired within the store's margin), nil otherwise.
func usableToken(token *pkgoauth.Token) *pkgoauth.Token {
	if token == nil || token.AccessToken == "" || token.IsExpiredWithMargin(tokenExpiryMargin) {
		return nil
	}
	return token
}

// SubjectGrant returns the person's grant for a subject-scoped issuer,
// refreshed when it has expired or is about to. The grant filed under the
// subject key is the canonical copy: it is what gets refreshed, and the
// rotated token is written back to it and to every session copy of the
// person, so no session redeems the old refresh token again (GitHub and
// GitLab rotate refresh tokens on use and reject a second redemption).
// Refreshes are single-flighted per person and issuer within a process,
// serialized across replicas by the shared store's refresh lock, and the
// store is re-read inside the flight, so concurrent lookups -- several
// sessions, several tool calls, several replicas -- cost one token request.
//
// Returns nil when the person holds no grant, when the grant has expired and
// carries no refresh token, or when the authorization server rejected the
// refresh token; in the last case the grant is removed for the person, so
// the next use starts a fresh sign-in instead of retrying a dead token.
func (c *Client) SubjectGrant(ctx context.Context, userID, issuer string) *pkgoauth.Token {
	issuer = strings.TrimSuffix(issuer, "/")
	if userID == "" || !c.subjectScoped(issuer) {
		return nil
	}
	grant := c.tokenStore.GetByIssuerIncludingExpired(subjectSessionID(userID), issuer)
	if grant == nil || grant.AccessToken == "" {
		return nil
	}
	if !refreshDue(grant) {
		return grant
	}
	if grant.RefreshToken == "" {
		return usableToken(grant)
	}

	result, _, _ := c.refreshGroup.Do(issuer+"|"+userID, func() (any, error) {
		return c.coordinatedRefresh(context.WithoutCancel(ctx), userID, issuer), nil
	})
	token, _ := result.(*pkgoauth.Token)
	return token
}

// coordinatedRefresh runs one refresh flight under the token store's
// cross-replica refresh lock, when the store is shared between replicas.
// The replica that takes the lock first redeems the refresh token; the
// others wait for it and then, re-reading the store inside the lock, find
// the grant fresh and return it without a token request. A provider that
// rotates refresh tokens on use (GitHub, GitLab) rejects a second
// redemption, so two replicas redeeming the same token would cost the
// person the grant.
func (c *Client) coordinatedRefresh(ctx context.Context, userID, issuer string) *pkgoauth.Token {
	if locker, ok := c.tokenStore.(grantRefreshLocker); ok {
		waitCtx, cancelWait := context.WithTimeout(ctx, grantRefreshLockTTL)
		unlock, err := locker.LockGrantRefresh(waitCtx, userID, issuer)
		cancelWait()
		if err != nil {
			// Redeeming without the lock risks a double redemption; the
			// access token, while still accepted, serves until the next
			// lookup tries again.
			logging.WarnWithAttrs("OAuth", "subject_grant_refresh_failed",
				slog.String("issuer", issuer),
				slog.String("sub", logging.TruncateIdentifier(userID)),
				slog.String("error", err.Error()))
			return usableToken(c.tokenStore.GetByIssuerIncludingExpired(subjectSessionID(userID), issuer))
		}
		defer unlock()
	}

	refreshCtx, cancel := context.WithTimeout(ctx, subjectGrantRefreshTimeout)
	defer cancel()
	return c.refreshSubjectGrant(refreshCtx, userID, issuer)
}

// refreshSubjectGrant is the body of one refresh flight: it re-reads the
// grant (another flight, or another replica, may have refreshed it since the
// caller looked, or while this one waited for the lock), redeems the refresh token with the client identification
// the grant was issued under at the issuer's token endpoint (pinned or
// discovered), and stores the rotated token under the subject key and every
// session copy.
func (c *Client) refreshSubjectGrant(ctx context.Context, userID, issuer string) *pkgoauth.Token {
	subjectKey := subjectSessionID(userID)
	current := c.tokenStore.GetByIssuerIncludingExpired(subjectKey, issuer)
	if current == nil || current.AccessToken == "" {
		return nil
	}
	if !refreshDue(current) {
		return current
	}
	if current.RefreshToken == "" {
		return usableToken(current)
	}

	pin := c.issuerPin(issuer)
	metadata, err := c.metadataFor(ctx, issuer, pin)
	if err != nil {
		logging.WarnWithAttrs("OAuth", "subject_grant_refresh_failed",
			slog.String("issuer", issuer),
			slog.String("sub", logging.TruncateIdentifier(userID)),
			slog.String("error", "authorization server metadata unavailable: "+err.Error()))
		return usableToken(current)
	}
	resolved := c.resolveClient(ctx, issuer, pin, metadata, false)
	if resolved.Method == ClientIDMethodNone {
		logging.WarnWithAttrs("OAuth", "subject_grant_refresh_failed",
			slog.String("issuer", issuer),
			slog.String("sub", logging.TruncateIdentifier(userID)),
			slog.String("error", resolved.RegistrationError.Error()))
		return usableToken(current)
	}

	fresh, err := c.oauthClient.RefreshToken(ctx, metadata.TokenEndpoint, current.RefreshToken, resolved.ClientID, resolved.ClientSecret)
	if err != nil {
		if pkgoauth.IsRefreshTokenRejected(err) {
			if stored, replaced := c.replacedSince(subjectKey, issuer, current.RefreshToken); replaced {
				// Another writer -- a replica without the shared lock, a
				// new sign-in -- replaced the grant while this request was
				// in flight: the grant lives on in its copy, which the
				// next lookup refreshes if it is due.
				logging.InfoWithAttrs("OAuth", "subject_grant_rotated_elsewhere",
					slog.String("issuer", issuer),
					slog.String("sub", logging.TruncateIdentifier(userID)))
				return usableToken(stored)
			}
			// The refresh token is dead: revoked at the authorization
			// server or expired. Nothing here can revive the grant; remove
			// it so the next use asks the person to sign in again.
			logging.WarnWithAttrs("OAuth", "subject_grant_refresh_rejected",
				slog.String("issuer", issuer),
				slog.String("sub", logging.TruncateIdentifier(userID)),
				slog.String("error", err.Error()))
			c.tokenStore.DeleteByUserAndIssuer(userID, issuer)
			return nil
		}
		logging.WarnWithAttrs("OAuth", "subject_grant_refresh_failed",
			slog.String("issuer", issuer),
			slog.String("sub", logging.TruncateIdentifier(userID)),
			slog.String("error", err.Error()))
		return usableToken(current)
	}

	fresh.Issuer = issuer
	if fresh.Scope == "" {
		fresh.Scope = current.Scope
	}
	if fresh.RefreshToken == "" {
		// The authorization server does not rotate refresh tokens; the one
		// we redeemed stays valid.
		fresh.RefreshToken = current.RefreshToken
	}
	if fresh.IDToken == "" {
		fresh.IDToken = current.IDToken
	}
	fresh.SetExpiresAtFromExpiresIn()

	replaced := c.tokenStore.ReplaceByUserAndIssuer(userID, issuer, fresh)
	if replaced == 0 {
		// The sweep found no copy under the person (an entry stored without
		// a user id, say); the grant itself must still carry the new token.
		c.tokenStore.Store(TokenKey{SessionID: subjectKey, Issuer: issuer, Scope: fresh.Scope}, fresh, userID)
	}

	logging.InfoWithAttrs("OAuth", "subject_grant_refreshed",
		slog.String("issuer", issuer),
		slog.String("sub", logging.TruncateIdentifier(userID)),
		slog.String("clientIdMethod", resolved.Method),
		slog.Bool("refreshTokenRotated", fresh.RefreshToken != current.RefreshToken),
		slog.Int("copies", replaced),
		slog.Time("expiresAt", fresh.ExpiresAt))
	return fresh
}

// replacedSince returns the stored grant and true when the store no longer
// holds the refresh token that was redeemed: the rejection is then about a
// token that was rotated away, and the grant must not be deleted.
func (c *Client) replacedSince(subjectKey, issuer, redeemed string) (*pkgoauth.Token, bool) {
	stored := c.tokenStore.GetByIssuerIncludingExpired(subjectKey, issuer)
	if stored == nil || stored.RefreshToken == redeemed {
		return nil, false
	}
	return stored, true
}

// refreshGroup single-flights grant refreshes per issuer and person.
type refreshGroup = singleflight.Group
