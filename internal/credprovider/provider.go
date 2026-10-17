package credprovider

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
	"github.com/giantswarm/muster/v5/pkg/proto/credproviderpb"
)

const (
	// DefaultAuthority is the credential URI authority muster answers.
	DefaultAuthority = "muster.giantswarm.io"
	// DefaultBindingTTL is the platform's default Session idle TTL.
	DefaultBindingTTL = 14 * 24 * time.Hour

	// useGit answers Basic credentials for git over HTTPS.
	useGit = "git"
	// useAPI answers the bare token for a Bearer API call.
	useAPI = "api"

	// gitUser is the user name GitHub takes with a token as password.
	gitUser = "x-access-token"

	// actorSPIFFEPrefix is the form of every Substrate actor identity.
	actorSPIFFEPrefix = "spiffe://substrate-actor.local/actor/"

	logSubsystem = "CredentialProvider"
)

// GrantSource returns the person's current grant from the issuer, refreshed
// when due, or nil when they hold none.
type GrantSource func(ctx context.Context, subject, issuer string) *api.OAuthToken

// Provider answers FetchSecret from the Session bindings.
type Provider struct {
	credproviderpb.UnimplementedCredentialProviderServer

	authority  string
	gateway    string
	releasable map[string]string
	ttl        time.Duration
	store      Store
	grants     GrantSource
}

// Options configure a Provider.
type Options struct {
	// Authority is the credential URI authority (DefaultAuthority if empty).
	Authority string
	// GatewayIdentity is the only caller SPIFFE id answered.
	GatewayIdentity string
	// Releasable maps provider instance to grant issuer.
	Releasable map[string]string
	// BindingTTL is the binding lifetime without use (DefaultBindingTTL if 0).
	BindingTTL time.Duration
	Store      Store
	Grants     GrantSource
}

// NewProvider returns a Provider, or an error for an incomplete configuration.
func NewProvider(o Options) (*Provider, error) {
	if o.GatewayIdentity == "" {
		return nil, fmt.Errorf("credential provider: gatewayIdentity is required")
	}
	if err := validateSPIFFE(o.GatewayIdentity); err != nil {
		return nil, fmt.Errorf("credential provider: gatewayIdentity: %w", err)
	}
	if len(o.Releasable) == 0 {
		return nil, fmt.Errorf("credential provider: releasable lists no provider")
	}
	releasable := make(map[string]string, len(o.Releasable))
	for provider, issuer := range o.Releasable {
		if provider == "" || strings.Contains(provider, "/") || issuer == "" {
			return nil, fmt.Errorf("credential provider: releasable entry %q: %q is not a provider name and issuer", provider, issuer)
		}
		releasable[provider] = strings.TrimSuffix(issuer, "/")
	}
	if o.Store == nil || o.Grants == nil {
		return nil, fmt.Errorf("credential provider: a binding store and a grant source are required")
	}
	p := &Provider{
		authority:  o.Authority,
		gateway:    o.GatewayIdentity,
		releasable: releasable,
		ttl:        o.BindingTTL,
		store:      o.Store,
		grants:     o.Grants,
	}
	if p.authority == "" {
		p.authority = DefaultAuthority
	}
	if p.ttl <= 0 {
		p.ttl = DefaultBindingTTL
	}
	return p, nil
}

// GatewayIdentity is the caller SPIFFE id the listener requires.
func (p *Provider) GatewayIdentity() string { return p.gateway }

// FetchSecret implements credproviderpb.CredentialProviderServer.
func (p *Provider) FetchSecret(ctx context.Context, req *credproviderpb.FetchSecretRequest) (*credproviderpb.FetchSecretResponse, error) {
	actor := req.GetActorSpiffeId()
	refuse := func(code codes.Code, reason string, attrs ...slog.Attr) error {
		logging.WarnWithAttrs(logSubsystem, "credential_refused", append([]slog.Attr{
			slog.String("reason", reason),
			slog.String("actor", actor),
			slog.String("uri", req.GetUri()),
		}, attrs...)...)
		return status.Error(code, reason)
	}

	if caller := callerIdentity(ctx); caller != p.gateway {
		return nil, refuse(codes.PermissionDenied, "caller is not the egress gateway", slog.String("caller", caller))
	}
	provider, use, err := p.parseURI(req.GetUri())
	if err != nil {
		return nil, refuse(codes.InvalidArgument, err.Error())
	}
	issuer, ok := p.releasable[provider]
	if !ok {
		return nil, refuse(codes.PermissionDenied, "provider is not releasable", slog.String("provider", provider))
	}
	if !strings.HasPrefix(actor, actorSPIFFEPrefix) {
		return nil, refuse(codes.InvalidArgument, "actor is not a Substrate actor SPIFFE id")
	}

	binding, err := p.store.Get(ctx, actor)
	if err != nil {
		logging.WarnWithAttrs(logSubsystem, "binding_lookup_failed", slog.String("actor", actor), slog.String("error", err.Error()))
		return nil, status.Error(codes.Unavailable, "binding store unavailable")
	}
	if binding == nil {
		return nil, refuse(codes.PermissionDenied, "actor is not bound")
	}
	who := []slog.Attr{slog.String("sub", binding.Subject), slog.String("session", binding.Session), slog.String("provider", provider)}
	if !binding.Allows(provider) {
		return nil, refuse(codes.PermissionDenied, "provider is not in the binding", who...)
	}

	token := p.grants(ctx, binding.Subject, issuer)
	if token == nil || token.AccessToken == "" {
		return nil, refuse(codes.NotFound, "the person holds no grant for the provider", who...)
	}
	if err := p.store.Touch(ctx, actor, p.ttl); err != nil {
		logging.WarnWithAttrs(logSubsystem, "binding_touch_failed", slog.String("actor", actor), slog.String("error", err.Error()))
	}

	secret := token.AccessToken
	if use == useGit {
		secret = base64.StdEncoding.EncodeToString([]byte(gitUser + ":" + token.AccessToken))
	}
	logging.InfoWithAttrs(logSubsystem, "credential_released", append(who,
		slog.String("actor", actor),
		slog.String("use", use),
		slog.Time("expiresAt", token.ExpiresAt))...)
	return &credproviderpb.FetchSecretResponse{OpaqueBytes: []byte(secret)}, nil
}

// parseURI splits ate-secret://<authority>/<provider>/<git|api>.
func (p *Provider) parseURI(raw string) (provider, use string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("credential URI does not parse")
	}
	if u.Scheme != "ate-secret" || u.Host != p.authority || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("credential URI is not ate-secret://%s/<provider>/<git|api>", p.authority)
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != useGit && parts[1] != useAPI) {
		return "", "", fmt.Errorf("credential URI is not ate-secret://%s/<provider>/<git|api>", p.authority)
	}
	return parts[0], parts[1], nil
}

// validateSPIFFE checks a spiffe://<trust domain>/<path> URI.
func validateSPIFFE(id string) error {
	u, err := url.Parse(id)
	if err != nil || u.Scheme != "spiffe" || u.Host == "" || u.Path == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%q is not a SPIFFE id", id)
	}
	return nil
}
