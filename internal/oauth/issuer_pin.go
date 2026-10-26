package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// IssuerPin is what an operator configured for one authorization server on an
// MCPServer (spec.auth.authorizationServer) beyond the issuer itself: a
// pre-registered client, whether the tokens the AS issues belong to the
// person (subject) or to the login session that obtained them, and the
// issuer identifier the AS puts in the RFC 9207 iss parameter when the pinned
// identity is not it.
type IssuerPin struct {
	ClientID      string
	ClientSecret  string
	SubjectScoped bool

	// ExpectedIssuer is what a present iss on the authorization response has
	// to equal instead of the pinned identity; empty for the identity itself.
	// The identity stays the grant key either way.
	ExpectedIssuer string

	// RevocationEndpoint is the RFC 7009 endpoint a disconnect revokes the
	// person's grant at, in place of the one the metadata advertises; empty
	// to use the metadata's.
	RevocationEndpoint string

	// metadata stands in for the discovery document of an authorization
	// server that publishes none (GitHub); nil to discover. Set by PinIssuer.
	metadata *pkgoauth.Metadata

	// digest identifies the description, see pinDigest. Set by PinIssuer.
	digest string
}

// expectedIssuer returns the issuer identifier the pinned authorization
// server puts in the RFC 9207 iss parameter, or "" when the pin names none
// (or there is no pin) and the pinned identity itself is expected.
func (p *IssuerPin) expectedIssuer() string {
	if p == nil {
		return ""
	}
	return p.ExpectedIssuer
}

// pinDigest identifies the description of the authorization server a flow
// starts under: its endpoints, its client and how its grants are filed. The
// client secret is left out, so a rotated secret does not end the flows in
// progress. Empty without a pin.
func pinDigest(pin *IssuerPin) string {
	if pin == nil {
		return ""
	}
	return pin.digest
}

func (p *IssuerPin) computeDigest() string {
	fields := []string{p.ClientID, strconv.FormatBool(p.SubjectScoped), p.ExpectedIssuer, p.RevocationEndpoint}
	if p.metadata != nil {
		fields = append(fields, p.metadata.AuthorizationEndpoint, p.metadata.TokenEndpoint)
	}
	sum := sha256.Sum256([]byte(strings.Join(fields, "\x00")))
	return hex.EncodeToString(sum[:])
}

// PinIssuer records the operator's description of an authorization server:
// the pre-registered client to identify with, whether its grants are
// subject-scoped, and -- for an AS without a discovery document -- its
// metadata. Calling it again for the same issuer replaces the whole
// description at once, which is how a rotated client secret takes effect.
//
// When the pinned document lists no code_challenge_methods_supported, S256 is
// assumed: the operator vouching for the endpoints is the only source of that
// fact, and MCP's refusal rule exists for servers that could have advertised
// it.
func (c *Client) PinIssuer(issuer string, pin IssuerPin, metadata *pkgoauth.Metadata) {
	issuer = strings.TrimSuffix(issuer, "/")
	if metadata != nil {
		pinned := *metadata
		if pinned.Issuer == "" {
			pinned.Issuer = api.IssuerURL(issuer)
		}
		if len(pinned.CodeChallengeMethodsSupported) == 0 {
			pinned.CodeChallengeMethodsSupported = []string{"S256"}
		}
		pin.metadata = &pinned
	}
	pin.digest = pin.computeDigest()
	c.pinsMu.Lock()
	c.pins[issuer] = &pin
	c.pinsMu.Unlock()
	logging.Info("OAuth", "Pinned authorization server issuer=%s preregisteredClient=%t subjectScoped=%t pinnedMetadata=%t expectedIssuer=%q revocationEndpoint=%q",
		issuer, pin.ClientID != "", pin.SubjectScoped, metadata != nil, pin.ExpectedIssuer, pin.RevocationEndpoint)
}

// UnpinIssuer forgets the operator's description of an authorization server:
// the pre-registered client, the grant scope and any pinned metadata. The next
// flow against the issuer discovers the AS again and identifies to it as it
// would without a pin. Called when no MCPServer describes the issuer any more.
func (c *Client) UnpinIssuer(issuer string) {
	issuer = strings.TrimSuffix(issuer, "/")
	c.pinsMu.Lock()
	_, hadPin := c.pins[issuer]
	delete(c.pins, issuer)
	c.pinsMu.Unlock()
	if hadPin {
		logging.Info("OAuth", "Unpinned authorization server issuer=%s", issuer)
	}
}

// issuerPin returns the operator pin for an issuer key, or nil. An operation
// reads it once and takes the endpoints (metadataFor) and the client
// (resolveClient) from that one pin.
func (c *Client) issuerPin(issuer string) *IssuerPin {
	c.pinsMu.RLock()
	defer c.pinsMu.RUnlock()
	return c.pins[strings.TrimSuffix(issuer, "/")]
}

// metadataFor returns the metadata of the authorization server behind an
// issuer key: the pin's own document when it has one, else the document the
// plain issuer publishes. A pin is filed under its key only, so a server
// outside muster's namespace never answers from a pin of muster's namespace.
func (c *Client) metadataFor(ctx context.Context, issuerKey string, pin *IssuerPin) (*pkgoauth.Metadata, error) {
	if pin != nil && pin.metadata != nil {
		return pin.metadata, nil
	}
	return c.oauthClient.DiscoverMetadata(ctx, api.IssuerURL(issuerKey))
}

// subjectScoped reports whether grants from the issuer belong to the person.
func (c *Client) subjectScoped(issuer string) bool {
	pin := c.issuerPin(issuer)
	return pin != nil && pin.SubjectScoped
}
