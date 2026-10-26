package oauth

import (
	"context"
	"log/slog"
	"time"

	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// subjectGrantRevokeTimeout bounds the revocation round trips of one
// disconnect: an authorization server that does not answer must not hold the
// person's sign-out.
const subjectGrantRevokeTimeout = 10 * time.Second

// revokeSubjectGrant revokes the person's grant for a subject-scoped issuer at
// the authorization server (RFC 7009): the refresh token first, which ends the
// grant at providers that tie both tokens to it (GitLab), then the access
// token, each identified with the client the grant was issued under. The
// endpoint is the operator's pinned one, else the one the metadata
// advertises.
//
// Every outcome is logged with the issuer and the person, none is returned:
// the disconnect drops muster's copy whether or not the provider took the
// revocation. An authorization server without a revocation endpoint keeps
// the grant on its side, as it did before.
func (c *Client) revokeSubjectGrant(userID, issuer string) {
	grant := c.tokenStore.GetByIssuerIncludingExpired(subjectSessionID(userID), issuer)
	if grant == nil || (grant.AccessToken == "" && grant.RefreshToken == "") {
		return
	}
	log := func(level func(string, string, ...slog.Attr), event string, extra ...slog.Attr) {
		level("OAuth", event, append([]slog.Attr{
			slog.String("issuer", issuer),
			slog.String("sub", logging.TruncateIdentifier(userID)),
		}, extra...)...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), subjectGrantRevokeTimeout)
	defer cancel()

	pin := c.issuerPin(issuer)
	metadata, err := c.metadataFor(ctx, issuer, pin)
	if err != nil {
		log(logging.WarnWithAttrs, "subject_grant_revocation_failed", slog.String("error", "authorization server metadata unavailable: "+err.Error()))
		return
	}
	endpoint := revocationEndpoint(pin, metadata)
	if endpoint == "" {
		log(logging.InfoWithAttrs, "subject_grant_revocation_unsupported")
		return
	}
	resolved := c.resolveClient(ctx, issuer, pin, metadata, false)

	tokens := []struct{ value, hint string }{
		{grant.RefreshToken, pkgoauth.FormFieldRefreshToken},
		{grant.AccessToken, "access_token"},
	}
	for _, t := range tokens {
		if t.value == "" {
			continue
		}
		if err := c.oauthClient.RevokeToken(ctx, endpoint, t.value, t.hint, resolved.ClientID, resolved.ClientSecret); err != nil {
			log(logging.WarnWithAttrs, "subject_grant_revocation_failed",
				slog.String("tokenType", t.hint), slog.String("error", err.Error()))
			continue
		}
		log(logging.InfoWithAttrs, "subject_grant_revoked",
			slog.String("tokenType", t.hint), slog.String("clientIdMethod", resolved.Method))
	}
}

// revocationEndpoint returns the RFC 7009 endpoint of an authorization server:
// the operator's pin, else the metadata's revocation_endpoint, empty when
// there is none.
func revocationEndpoint(pin *IssuerPin, metadata *pkgoauth.Metadata) string {
	if pin != nil && pin.RevocationEndpoint != "" {
		return pin.RevocationEndpoint
	}
	return metadata.RevocationEndpoint
}
