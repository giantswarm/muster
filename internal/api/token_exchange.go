package api

import "github.com/giantswarm/mcp-oauth/providers/dex"

// TokenExchangeConfig mixes spec fields (from the MCPServer CR) with
// runtime-resolved state: the client credentials loaded from a Secret and the
// requiredAudiences appended to Scopes per connection. The two methods below
// are the single definition of which fields are which — every site that stamps
// or strips runtime state must go through them, never hand-roll the copy.
//
// The registry's MCPServer definition shares this struct by pointer with the
// connection paths, and MCPServerReconciler.ConfigurationChanged compares that
// definition against the CR. Stamping runtime state onto the shared struct in
// place makes the stored definition permanently differ from the CR, so every
// reconcile sees a "configuration changed" and restarts the server (~10-15s
// churn, giantswarm/giantswarm#37060).

// ResolvedTokenExchangeConfig is a TokenExchangeConfig that carries the
// per-connection runtime state. It is only produced by WithResolvedRuntime, so
// a function that needs the resolved credentials and audience scopes can
// require this type and the shared spec-only registry definition cannot be
// passed by mistake (the bug class fixed in #944).
type ResolvedTokenExchangeConfig struct {
	TokenExchangeConfig
}

// WithResolvedRuntime returns a copy of the config carrying the per-connection
// runtime state: the resolved client credentials and, when requiredAudiences is
// non-empty, the appended cross-client audience scopes. The value receiver
// guarantees the receiver (shared with the registry definition) is never
// mutated.
//
// On an audience-scope formatting error the credential-populated copy is
// returned (without audiences) together with the error, so callers can log and
// continue.
func (c TokenExchangeConfig) WithResolvedRuntime(clientID, clientSecret string, requiredAudiences []string) (ResolvedTokenExchangeConfig, error) {
	c.ClientID = clientID
	c.ClientSecret = clientSecret
	if len(requiredAudiences) > 0 {
		updatedScopes, err := dex.AppendAudienceScopes(c.Scopes, requiredAudiences)
		if err != nil {
			return ResolvedTokenExchangeConfig{c}, err
		}
		c.Scopes = updatedScopes
	}
	return ResolvedTokenExchangeConfig{c}, nil
}

// SpecOnly returns a copy of the config with the runtime-resolved fields
// cleared, so spec-derived fields can be compared against a definition freshly
// rebuilt from the CR (where those fields are always empty — they are tagged
// json:"-" yaml:"-").
func (c TokenExchangeConfig) SpecOnly() TokenExchangeConfig {
	c.ClientID = ""
	c.ClientSecret = ""
	return c
}
