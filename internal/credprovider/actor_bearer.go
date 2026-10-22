package credprovider

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/giantswarm/mcp-oauth/providers/oidc"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

const (
	// MusterProvider is the reserved provider instance a binding lists to let
	// its actor call muster itself as the bound person with Substrate's actor
	// JWT. FetchSecret never releases it.
	MusterProvider = "muster"

	// ActorJWKSPath is where Substrate serves its actor-JWT key set,
	// relative to the issuer.
	ActorJWKSPath = "/openid/v1/jwks"

	// actorSubjectPrefix is the form of an actor JWT's sub:
	// actor/<atespace>/<actor>.
	actorSubjectPrefix = "actor/"

	// actorSessionPrefix marks the muster session of an actor-JWT caller.
	actorSessionPrefix = "actor-"

	// actorBearerAuditAction is the audit action of every actor-JWT
	// authentication, accepted or refused.
	actorBearerAuditAction = "actor_bearer"
)

var (
	// ErrInvalidActorToken is a bearer that is no valid actor JWT for muster.
	ErrInvalidActorToken = errors.New("invalid actor token")
	// ErrActorNotAllowed is a valid actor JWT whose actor is unbound or whose
	// binding does not list MusterProvider.
	ErrActorNotAllowed = errors.New("actor may not call muster")
	// ErrBindingsUnavailable is a binding store failure.
	ErrBindingsUnavailable = errors.New("binding store unavailable")
)

// ActorToken is what muster reads of a verified actor JWT.
type ActorToken struct {
	// Subject is the sub: actor/<atespace>/<actor>.
	Subject string
	// ActorUID is the Substrate uid of the actor, which changes when the
	// actor is recreated under the same name.
	ActorUID string
	// Expiry is the exp.
	Expiry time.Time
}

// ActorTokenVerifier verifies an actor JWT's signature, issuer, audience and
// lifetime, and returns what muster reads of it.
type ActorTokenVerifier func(ctx context.Context, token string) (ActorToken, error)

// ActorCaller is the person an actor-JWT bearer acts as.
type ActorCaller struct {
	// Actor is the actor's SPIFFE id.
	Actor string
	// Subject is the bound person.
	Subject string
	// Session is the bound Session, for the audit log.
	Session string
	// SessionID is the muster session of the actor under its binding:
	// stable across its turns.
	SessionID string
	// Expiry is the actor JWT's exp.
	Expiry time.Time
	// Toolset is the bound toolset, the selectors every request of the actor
	// resolves within; nil when the binding carries none.
	Toolset []string
}

// ActorBearer authenticates Substrate actor JWTs as the person the actor is
// bound to.
type ActorBearer struct {
	issuer string
	verify ActorTokenVerifier
	store  Store
	ttl    time.Duration
}

// ActorBearerOptions configure an ActorBearer.
type ActorBearerOptions struct {
	// Issuer is the actor JWTs' iss. Required.
	Issuer string
	// Verify checks a JWT of Issuer. Required.
	Verify ActorTokenVerifier
	// Store holds the Session bindings. Required.
	Store Store
	// BindingTTL restarts a binding on each accepted request
	// (DefaultBindingTTL if 0).
	BindingTTL time.Duration
}

// NewActorBearer returns an ActorBearer, or an error for an incomplete
// configuration.
func NewActorBearer(o ActorBearerOptions) (*ActorBearer, error) {
	if err := validateIssuer(o.Issuer); err != nil {
		return nil, fmt.Errorf("actor bearer: %w", err)
	}
	if o.Verify == nil || o.Store == nil {
		return nil, fmt.Errorf("actor bearer: a verifier and a binding store are required")
	}
	ttl := o.BindingTTL
	if ttl <= 0 {
		ttl = DefaultBindingTTL
	}
	return &ActorBearer{issuer: o.Issuer, verify: o.Verify, store: o.Store, ttl: ttl}, nil
}

// Claims reports whether bearer is a JWT naming the actor-JWT issuer. It reads
// the payload unverified and only routes the request: Authenticate verifies.
func (a *ActorBearer) Claims(bearer string) bool {
	parts := strings.Split(bearer, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Issuer string `json:"iss"`
	}
	return json.Unmarshal(payload, &claims) == nil && claims.Issuer == a.issuer
}

// Authenticate verifies bearer and returns the person its actor is bound to.
// It fails with ErrInvalidActorToken, ErrActorNotAllowed or
// ErrBindingsUnavailable.
func (a *ActorBearer) Authenticate(ctx context.Context, bearer string) (*ActorCaller, error) {
	token, err := a.verify(ctx, bearer)
	if err != nil {
		return nil, a.refuse(ErrInvalidActorToken, err.Error(), "", nil)
	}
	actor, err := actorFromSubject(token.Subject)
	if err != nil {
		return nil, a.refuse(ErrInvalidActorToken, err.Error(), token.Subject, nil)
	}
	if token.ActorUID == "" {
		return nil, a.refuse(ErrInvalidActorToken, "the token carries no actor uid", actor, nil)
	}
	binding, err := a.store.Get(ctx, actor)
	if err != nil {
		logging.WarnWithAttrs(logSubsystem, "binding_lookup_failed", slog.String("actor", actor), slog.String("error", err.Error()))
		return nil, ErrBindingsUnavailable
	}
	if binding == nil {
		return nil, a.refuse(ErrActorNotAllowed, "actor is not bound", actor, nil)
	}
	if !binding.Allows(MusterProvider) {
		return nil, a.refuse(ErrActorNotAllowed, "the binding does not list "+MusterProvider, actor, binding)
	}
	if binding.ActorUID != token.ActorUID {
		err := a.store.PinActorUID(ctx, actor, token.ActorUID)
		if errors.Is(err, ErrActorUIDMismatch) {
			return nil, a.refuse(ErrActorNotAllowed, "actor uid "+token.ActorUID+" is not the uid the binding is pinned to", actor, binding)
		}
		if err != nil {
			logging.WarnWithAttrs(logSubsystem, "binding_pin_failed", slog.String("actor", actor), slog.String("error", err.Error()))
			return nil, ErrBindingsUnavailable
		}
	}
	if err := a.store.Touch(ctx, actor, a.ttl); err != nil {
		logging.WarnWithAttrs(logSubsystem, "binding_touch_failed", slog.String("actor", actor), slog.String("error", err.Error()))
	}
	// The session is the actor's under this binding: a rebound actor never
	// inherits another binding's muster session.
	sum := sha256.Sum256([]byte(actor + "\x00" + binding.Subject + "\x00" + binding.Session))
	logging.Audit(logging.AuditEvent{
		Action:  actorBearerAuditAction,
		Outcome: "success",
		Subject: logging.TruncateIdentifier(binding.Subject),
		Target:  MusterProvider,
		Details: actorAuditDetails(actor, binding),
	})
	return &ActorCaller{
		Actor:     actor,
		Subject:   binding.Subject,
		Session:   binding.Session,
		SessionID: actorSessionPrefix + hex.EncodeToString(sum[:16]),
		Expiry:    token.Expiry,
		Toolset:   binding.Toolset,
	}, nil
}

func (a *ActorBearer) refuse(kind error, reason, actor string, binding *Binding) error {
	event := logging.AuditEvent{
		Action:  actorBearerAuditAction,
		Outcome: "failure",
		Target:  MusterProvider,
		Details: actorAuditDetails(actor, binding),
		Error:   reason,
	}
	if binding != nil {
		event.Subject = logging.TruncateIdentifier(binding.Subject)
	}
	logging.Audit(event)
	return fmt.Errorf("%w: %s", kind, reason)
}

// actorAuditDetails names the actor and, when it has them, the Session it is
// bound to and the toolset bound with it.
func actorAuditDetails(actor string, binding *Binding) string {
	details := "actor=" + actor
	if binding != nil && binding.Session != "" {
		details += " session=" + binding.Session
	}
	if binding != nil && binding.Toolset != nil {
		details += " toolset=[" + strings.Join(binding.Toolset, ",") + "]"
	}
	return details
}

// IsActorSession reports whether a muster session ID is an actor-JWT
// caller's (ActorCaller.SessionID), which has no login of its own.
func IsActorSession(sessionID string) bool {
	return strings.HasPrefix(sessionID, actorSessionPrefix)
}

// actorFromSubject maps an actor JWT's sub, actor/<atespace>/<actor>, to the
// actor's SPIFFE id, the key its binding is filed under.
func actorFromSubject(sub string) (string, error) {
	rest, ok := strings.CutPrefix(sub, actorSubjectPrefix)
	parts := strings.Split(rest, "/")
	if !ok || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("sub %q is not actor/<atespace>/<actor>", sub)
	}
	return actorSPIFFEPrefix + rest, nil
}

// OIDCVerifierOptions configure NewOIDCActorTokenVerifier.
type OIDCVerifierOptions struct {
	// Issuer is the expected iss. Required.
	Issuer string
	// JwksURL is the key set (Issuer + ActorJWKSPath if empty).
	JwksURL string
	// Audience is the expected aud. Required.
	Audience string
	// RootCAs verifies the key set endpoint (system roots if nil).
	RootCAs *x509.CertPool
	// AllowPrivateIPHosts may resolve to a private address.
	AllowPrivateIPHosts []string
}

// NewOIDCActorTokenVerifier verifies actor JWTs against the issuer's key set:
// an asymmetric signature by a published key, exp (required), nbf and iat
// with clock-skew leeway, the issuer and the audience. The actor uid is read
// from the verified payload.
func NewOIDCActorTokenVerifier(o OIDCVerifierOptions) (ActorTokenVerifier, error) {
	if err := validateIssuer(o.Issuer); err != nil {
		return nil, fmt.Errorf("actor bearer: %w", err)
	}
	if o.Audience == "" {
		return nil, fmt.Errorf("actor bearer: audience is required")
	}
	jwksURL := o.JwksURL
	if jwksURL == "" {
		jwksURL = strings.TrimSuffix(o.Issuer, "/") + ActorJWKSPath
	}
	client := oidc.NewJWKSClientWithOptions(oidc.JWKSClientOptions{
		RootCAs:             o.RootCAs,
		AllowPrivateIPHosts: o.AllowPrivateIPHosts,
	})
	audiences := []string{o.Audience}
	return func(ctx context.Context, token string) (ActorToken, error) {
		claims, err := oidc.ValidateIDToken(ctx, token, client, jwksURL, o.Issuer, audiences)
		if err != nil {
			return ActorToken{}, err
		}
		uid, err := actorUIDClaim(token)
		if err != nil {
			return ActorToken{}, err
		}
		return ActorToken{Subject: claims.Subject, ActorUID: uid, Expiry: claims.Expiry.Time()}, nil
	}, nil
}

// actorUIDClaim reads the actor uid of a JWT whose signature was verified:
// actor.ate.dev.actorUID, as upstream Substrate mints it, or ate.dev.actorUID,
// its earlier name.
func actorUIDClaim(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("token is not a JWS compact serialization")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	type substrateClaims struct {
		ActorUID string `json:"actorUID"`
	}
	var claims struct {
		Actor  *substrateClaims `json:"actor.ate.dev"`
		Legacy *substrateClaims `json:"ate.dev"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	if claims.Actor != nil {
		return claims.Actor.ActorUID, nil
	}
	if claims.Legacy != nil {
		return claims.Legacy.ActorUID, nil
	}
	return "", nil
}

// validateIssuer checks an https issuer URL without query or fragment.
func validateIssuer(issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("issuer %q is not an https URL", issuer)
	}
	return nil
}
