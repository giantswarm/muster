package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ProviderAppFlowChatGPTDeviceCode is the flow of a provider App a person
// connects their ChatGPT subscription with: OpenAI's device-code sign-in.
const ProviderAppFlowChatGPTDeviceCode = "chatgptDeviceCode"

// ChatGPT sign-in defaults, the Codex CLI's: its public client and OpenAI's
// authorization server.
const (
	ChatGPTClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	ChatGPTIssuer   = "https://auth.openai.com"
	ChatGPTScopes   = "openid profile email offline_access"
)

// ProviderAppsWithDefaults returns the Apps with what a device-code App
// leaves out filled in (providerAppWithDefaults).
func ProviderAppsWithDefaults(apps []ProviderAppConfig) []ProviderAppConfig {
	if len(apps) == 0 {
		return apps
	}
	out := make([]ProviderAppConfig, len(apps))
	for i, app := range apps {
		out[i] = providerAppWithDefaults(app)
	}
	return out
}

// providerAppWithDefaults fills in what a device-code App leaves out: the
// Codex CLI's client, OpenAI's endpoints under the issuer, and the scope
// codex signs in with. Any other App is returned unchanged.
func providerAppWithDefaults(app ProviderAppConfig) ProviderAppConfig {
	if app.Flow != ProviderAppFlowChatGPTDeviceCode {
		return app
	}
	if app.Issuer == "" {
		app.Issuer = ChatGPTIssuer
	}
	issuer := strings.TrimSuffix(app.Issuer, "/")
	if app.ClientID == "" && app.ClientCredentialsSecretRef == nil {
		app.ClientID = ChatGPTClientID
	}
	if app.AuthorizationEndpoint == "" && app.TokenEndpoint == "" {
		app.AuthorizationEndpoint = issuer + "/oauth/authorize"
		app.TokenEndpoint = issuer + "/oauth/token"
	}
	if app.Scopes == "" {
		app.Scopes = ChatGPTScopes
	}
	return app
}

// providerAppName is the shape of a provider App's name: it is a credential
// URI path segment and the suffix of an HTTP header name.
var providerAppName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateProviderApps checks the provider Apps: a valid, unique name, an
// absolute issuer no other App files its grants under, endpoints set together,
// and a git user without a colon.
func ValidateProviderApps(apps []ProviderAppConfig) error {
	names := make(map[string]bool, len(apps))
	issuers := make(map[string]string, len(apps))
	for i, app := range apps {
		if !providerAppName.MatchString(app.Name) {
			return fmt.Errorf("providerApps[%d]: name %q must be lower-case letters, digits and dashes", i, app.Name)
		}
		if names[app.Name] {
			return fmt.Errorf("providerApps[%d]: name %q is used twice", i, app.Name)
		}
		names[app.Name] = true
		if err := absoluteURL(app.Issuer); err != nil {
			return fmt.Errorf("providerApps %s: issuer: %w", app.Name, err)
		}
		issuer := strings.TrimSuffix(app.Issuer, "/")
		if other, ok := issuers[issuer]; ok {
			return fmt.Errorf("providerApps %s: issuer %s is already the issuer of App %s", app.Name, issuer, other)
		}
		issuers[issuer] = app.Name
		if (app.AuthorizationEndpoint == "") != (app.TokenEndpoint == "") {
			return fmt.Errorf("providerApps %s: authorizationEndpoint and tokenEndpoint must be set together", app.Name)
		}
		if app.ExpectedIssuer != "" && app.AuthorizationEndpoint == "" {
			return fmt.Errorf("providerApps %s: expectedIssuer needs authorizationEndpoint and tokenEndpoint", app.Name)
		}
		for field, value := range map[string]string{
			"authorizationEndpoint": app.AuthorizationEndpoint,
			"tokenEndpoint":         app.TokenEndpoint,
			"expectedIssuer":        app.ExpectedIssuer,
			"revocationEndpoint":    app.RevocationEndpoint,
		} {
			if value == "" {
				continue
			}
			if err := absoluteURL(value); err != nil {
				return fmt.Errorf("providerApps %s: %s: %w", app.Name, field, err)
			}
		}
		if ref := app.ClientCredentialsSecretRef; ref != nil && ref.Name == "" {
			return fmt.Errorf("providerApps %s: clientCredentialsSecretRef.name is required", app.Name)
		}
		if app.ClientID != "" && app.ClientCredentialsSecretRef != nil {
			return fmt.Errorf("providerApps %s: set either clientID or clientCredentialsSecretRef, not both", app.Name)
		}
		switch app.Flow {
		case "":
		case ProviderAppFlowChatGPTDeviceCode:
			if app.ClientCredentialsSecretRef != nil {
				return fmt.Errorf("providerApps %s: flow %s signs in as a public client: set clientID, not clientCredentialsSecretRef", app.Name, app.Flow)
			}
		default:
			return fmt.Errorf("providerApps %s: flow %q is not one of \"\", %q", app.Name, app.Flow, ProviderAppFlowChatGPTDeviceCode)
		}
		if strings.Contains(app.GitUser, ":") {
			return fmt.Errorf("providerApps %s: gitUser %q must not contain a colon", app.Name, app.GitUser)
		}
	}
	return nil
}

// absoluteURL reports whether value is an absolute http(s) URL.
func absoluteURL(value string) error {
	u, err := url.Parse(value)
	if err != nil {
		return err
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%q is not an absolute http(s) URL", value)
	}
	return nil
}
