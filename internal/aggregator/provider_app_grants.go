package aggregator

import (
	"context"
	"fmt"
	"maps"

	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/giantswarm/muster/v5/internal/api"
	configPkg "github.com/giantswarm/muster/v5/internal/config"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// releasableWithProviderApps adds every provider App to the credential
// provider's releasable list under its name, with its git user. An entry the
// configuration already names is kept when it agrees with the App, and refused
// when it does not: one provider instance has one grant.
func releasableWithProviderApps(releasable, gitUsers map[string]string, apps []configPkg.ProviderAppConfig) (map[string]string, map[string]string, error) {
	if len(apps) == 0 {
		return releasable, gitUsers, nil
	}
	outReleasable := maps.Clone(releasable)
	if outReleasable == nil {
		outReleasable = make(map[string]string, len(apps))
	}
	outGitUsers := maps.Clone(gitUsers)
	if outGitUsers == nil {
		outGitUsers = make(map[string]string, len(apps))
	}
	for _, app := range apps {
		issuer := trimSlash(app.Issuer)
		if configured, ok := outReleasable[app.Name]; ok && trimSlash(configured) != issuer {
			return nil, nil, fmt.Errorf("releasable %s is %s, but provider App %s files its grants under %s", app.Name, configured, app.Name, issuer)
		}
		outReleasable[app.Name] = issuer
		if app.GitUser == "" {
			continue
		}
		if configured, ok := outGitUsers[app.Name]; ok && configured != app.GitUser {
			return nil, nil, fmt.Errorf("gitUsers %s is %q, but provider App %s names %q", app.Name, configured, app.Name, app.GitUser)
		}
		outGitUsers[app.Name] = app.GitUser
	}
	return outReleasable, outGitUsers, nil
}

// grantHeaderFunc returns the per-request header function that attaches the
// person's provider App tokens to a session's connection to a server with
// spec.auth.providerApps, or nil for every other server.
//
// Each request reads the server's current registry entry and the person's
// grant of each App it names, refreshed when due, and sends the access token
// of every App the person granted in pkgoauth.ProviderAppGrantHeader(app). An
// App the person has not granted sends no header: the server answers what it
// can without it, or names the App in a grant_required result.
func (a *AggregatorServer) grantHeaderFunc(sessionID, sub string, serverInfo *ServerInfo) transport.HTTPHeaderFunc {
	if serverInfo == nil || !serverInfo.AuthConfig.ReceivesProviderApps() || api.IsTenantMCPServerKey(serverInfo.Name) {
		return nil
	}
	name := serverInfo.Name
	return func(context.Context) map[string]string {
		info := serverInfo
		if current, ok := a.registry.GetServerInfo(name); ok {
			info = current
		}
		if !info.AuthConfig.ReceivesProviderApps() {
			return nil
		}
		handler := api.GetOAuthHandler()
		if handler == nil || !handler.IsEnabled() {
			return nil
		}
		headers := make(map[string]string, len(info.AuthConfig.ProviderApps))
		for _, appName := range info.AuthConfig.ProviderApps {
			app, ok := a.providerApps.get(appName)
			if !ok {
				continue
			}
			token := api.FullTokenByIssuerForUser(handler, sessionID, sub, app.Issuer)
			if token == nil || token.AccessToken == "" {
				continue
			}
			headers[pkgoauth.ProviderAppGrantHeader(appName)] = token.AccessToken
		}
		if len(headers) == 0 {
			return nil
		}
		return headers
	}
}

// sessionHeaderFunc is the per-request header function of a session's
// connection to a server: the session's ID token with spec.auth.forwardIdentity
// and the person's App tokens with spec.auth.providerApps, on top of the
// connection's own (base, nil for none).
func (a *AggregatorServer) sessionHeaderFunc(sessionID, sub string, serverInfo *ServerInfo, base transport.HTTPHeaderFunc) transport.HTTPHeaderFunc {
	return mergeHeaderFuncs(base, a.identityHeaderFunc(sessionID, serverInfo), a.grantHeaderFunc(sessionID, sub, serverInfo))
}

// mergeHeaderFuncs returns a header function sending the headers of every
// non-nil function, a later one's on top of an earlier one's; nil when none
// is set.
func mergeHeaderFuncs(fns ...transport.HTTPHeaderFunc) transport.HTTPHeaderFunc {
	var set []transport.HTTPHeaderFunc
	for _, fn := range fns {
		if fn != nil {
			set = append(set, fn)
		}
	}
	switch len(set) {
	case 0:
		return nil
	case 1:
		return set[0]
	}
	return func(ctx context.Context) map[string]string {
		var merged map[string]string
		for _, fn := range set {
			headers := fn(ctx)
			if len(headers) == 0 {
				continue
			}
			if merged == nil {
				merged = make(map[string]string, len(headers))
			}
			maps.Copy(merged, headers)
		}
		return merged
	}
}

// trimSlash drops an issuer's trailing slash, the form grants are keyed by.
func trimSlash(issuer string) string {
	for len(issuer) > 0 && issuer[len(issuer)-1] == '/' {
		issuer = issuer[:len(issuer)-1]
	}
	return issuer
}
