package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/giantswarm/muster/v5/internal/api"
	configPkg "github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// providerAppSet is the aggregator's provider Apps (aggregator.providerApps):
// each provider instance's OAuth App a person grants once in muster, apart from
// any MCPServer. Every App's issuer is pinned subject-scoped from muster's
// start, so the person's grant is found, refreshed and released whether or not
// an MCPServer naming it ever answered.
type providerAppSet struct {
	apps      map[string]configPkg.ProviderAppConfig
	order     []string
	namespace string

	mu sync.Mutex
	// pinned are the Apps whose pin carries their client: an App whose
	// Secret could not be read is pinned without it and tried again.
	pinned map[string]bool
}

// newProviderAppSet validates the configured Apps. namespace is muster's, the
// default of a client Secret reference.
func newProviderAppSet(apps []configPkg.ProviderAppConfig, namespace string) (*providerAppSet, error) {
	if err := configPkg.ValidateProviderApps(apps); err != nil {
		return nil, err
	}
	set := &providerAppSet{
		apps:      make(map[string]configPkg.ProviderAppConfig, len(apps)),
		namespace: namespace,
		pinned:    make(map[string]bool, len(apps)),
	}
	for _, app := range apps {
		app.Issuer = strings.TrimSuffix(app.Issuer, "/")
		set.apps[app.Name] = app
		set.order = append(set.order, app.Name)
	}
	return set, nil
}

// get returns the App of a name.
func (s *providerAppSet) get(name string) (configPkg.ProviderAppConfig, bool) {
	if s == nil {
		return configPkg.ProviderAppConfig{}, false
	}
	app, ok := s.apps[name]
	return app, ok
}

// names returns the App names in configuration order.
func (s *providerAppSet) names() []string {
	if s == nil {
		return nil
	}
	return s.order
}

// owns reports whether an App files its grants under the issuer key: an App
// pin wins over an MCPServer's pin of the same issuer.
func (s *providerAppSet) owns(issuerKey string) bool {
	if s == nil {
		return false
	}
	issuerKey = strings.TrimSuffix(issuerKey, "/")
	for _, app := range s.apps {
		if app.Issuer == issuerKey {
			return true
		}
	}
	return false
}

// pinAll pins every App not yet pinned with its client. An App whose Secret
// cannot be read is pinned subject-scoped without its client, so the grants
// people hold are still released, and is tried again on the next call.
func (s *providerAppSet) pinAll(ctx context.Context) {
	if s == nil {
		return
	}
	for _, name := range s.order {
		s.mu.Lock()
		done := s.pinned[name]
		s.mu.Unlock()
		if done {
			continue
		}
		if err := s.pin(ctx, name); err != nil {
			logging.Warn("Aggregator", "Provider App %s is pinned without its client, retrying: %v", name, err)
		}
	}
}

// pin hands one App's authorization server to the OAuth handler, its client
// read from its Secret now: a rotated Secret is picked up by the next sign-in.
func (s *providerAppSet) pin(ctx context.Context, name string) error {
	app, ok := s.get(name)
	if !ok {
		return fmt.Errorf("provider App %s is not configured", name)
	}
	handler := api.GetOAuthHandler()
	if handler == nil || !handler.IsEnabled() {
		return fmt.Errorf("provider App %s needs the OAuth proxy, which is disabled", name)
	}
	pinner, ok := handler.(api.IssuerPinner)
	if !ok {
		return fmt.Errorf("provider App %s: the OAuth handler cannot record pins", name)
	}
	pin := api.IssuerPin{
		AuthorizationEndpoint: app.AuthorizationEndpoint,
		TokenEndpoint:         app.TokenEndpoint,
		ExpectedIssuer:        app.ExpectedIssuer,
		SubjectScoped:         true,
		RevocationEndpoint:    app.RevocationEndpoint,
	}
	var loadErr error
	if ref := app.ClientCredentialsSecretRef; ref != nil {
		loadErr = fmt.Errorf("no secret handler is registered")
		if secrets := api.GetSecretCredentialsHandler(); secrets != nil {
			var creds *api.ClientCredentials
			creds, loadErr = secrets.LoadClientCredentials(ctx, &api.ClientCredentialsSecretRef{
				Name:            ref.Name,
				Namespace:       ref.Namespace,
				ClientIDKey:     ref.ClientIDKey,
				ClientSecretKey: ref.ClientSecretKey,
			}, s.namespace)
			if loadErr == nil {
				pin.ClientID, pin.ClientSecret = creds.ClientID, creds.ClientSecret
			}
		}
	}
	pinner.PinIssuer(app.Issuer, pin)
	s.mu.Lock()
	s.pinned[name] = loadErr == nil
	s.mu.Unlock()
	logging.InfoWithAttrs("Aggregator", "oauth_provider_app_pinned",
		slog.String("app", name),
		slog.String("issuer", app.Issuer),
		slog.Bool("preregisteredClient", pin.ClientID != ""),
		slog.Bool("pinnedEndpoints", app.AuthorizationEndpoint != ""),
		slog.String("revocationEndpoint", pin.RevocationEndpoint))
	if loadErr != nil {
		return fmt.Errorf("load the client of provider App %s: %w", name, loadErr)
	}
	return nil
}

// providerAppOfFlow returns the App name of an OAuth flow's server name, when
// the flow is an App's sign-in.
func providerAppOfFlow(serverName string) (string, bool) {
	return strings.CutPrefix(serverName, api.ProviderAppFlowPrefix)
}

// appOwnedIssuers are the issuer keys of the running aggregator's provider
// Apps, read by the MCPServer pin paths, which are not methods of the
// aggregator: an MCPServer neither replaces nor drops an App's pin.
var appOwnedIssuers struct {
	sync.RWMutex
	set *providerAppSet
}

// setAppOwnedIssuers records the running aggregator's provider Apps.
func setAppOwnedIssuers(set *providerAppSet) {
	appOwnedIssuers.Lock()
	defer appOwnedIssuers.Unlock()
	appOwnedIssuers.set = set
}

// appOwnsIssuer reports whether a provider App owns the issuer key.
func appOwnsIssuer(issuerKey string) bool {
	appOwnedIssuers.RLock()
	defer appOwnedIssuers.RUnlock()
	return appOwnedIssuers.set.owns(issuerKey)
}
