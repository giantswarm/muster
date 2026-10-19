package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// The stored OAuth state of an organization namespace goes with the
// namespace's servers (D9): the people's tokens and subject grants, the
// client registrations and the flows in progress, all filed under the
// namespace's issuer keys (api.IssuerKey). A later server in a namespace of
// the same name then starts with nothing. Pins are not stored: they are
// derived from the live MCPServer definitions and released with them
// (releaseAuthorizationServerPin).

// forgetScanCount is the SCAN page size of a forget over Valkey.
const forgetScanCount = 100

// tenantIssuerMatch matches the issuer keys of a namespace: every key of it
// when issuerKeys is empty, else exactly the given keys. It never matches a
// key of muster's namespace.
func tenantIssuerMatch(namespace string, issuerKeys []string) func(string) bool {
	if len(issuerKeys) == 0 {
		return func(key string) bool {
			ns, _ := api.SplitIssuerKey(key)
			return ns != "" && ns == namespace
		}
	}
	wanted := make(map[string]bool, len(issuerKeys))
	for _, key := range issuerKeys {
		if ns, _ := api.SplitIssuerKey(key); ns == namespace && ns != "" {
			wanted[key] = true
		}
	}
	return func(key string) bool { return wanted[key] }
}

// ForgetTenantIssuers removes the stored OAuth state of an organization
// namespace: tokens and subject grants of every session and person,
// client registrations and flows in progress. issuerKeys names the issuer
// keys to forget; empty forgets every key of the namespace. An empty
// namespace (muster's own) forgets nothing.
func (c *Client) ForgetTenantIssuers(ctx context.Context, namespace string, issuerKeys []string) error {
	if namespace == "" {
		return nil
	}
	match := tenantIssuerMatch(namespace, issuerKeys)
	var errs []string
	if err := c.tokenStore.DeleteIssuers(ctx, match); err != nil {
		errs = append(errs, "tokens: "+err.Error())
	}
	if err := c.credStore.DeleteIssuers(ctx, match); err != nil {
		errs = append(errs, "client registrations: "+err.Error())
	}
	if err := c.stateStore.DeleteIssuers(ctx, match); err != nil {
		errs = append(errs, "flows: "+err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("forget the OAuth state of namespace %s: %s", namespace, strings.Join(errs, "; "))
	}
	return nil
}

// DeleteIssuers removes every token whose issuer key matches, in every
// session and of every person.
func (ts *TokenStore) DeleteIssuers(_ context.Context, match func(string) bool) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for key := range ts.tokens {
		if match(key.Issuer) {
			delete(ts.tokens, key)
		}
	}
	return nil
}

// DeleteIssuers removes every token whose issuer key matches, in every
// session hash. The user index keeps its session IDs: it is a reverse lookup
// only, and its entries expire with the sessions.
func (s *ValkeyTokenStore) DeleteIssuers(ctx context.Context, match func(string) bool) error {
	tokenPrefix := s.keyPrefix + "oauth:token:"
	userPrefix := s.keyPrefix + "oauth:token:user:"
	return scanKeys(ctx, s.client, tokenPrefix+"*", func(key string) error {
		if strings.HasPrefix(key, userPrefix) {
			return nil
		}
		fields, err := s.client.Do(ctx, s.client.B().Hkeys().Key(key).Build()).AsStrSlice()
		if err != nil {
			return err
		}
		var matching []string
		for _, field := range fields {
			if issuer, _ := parseFieldName(field); match(issuer) {
				matching = append(matching, field)
			}
		}
		if len(matching) == 0 {
			return nil
		}
		return s.client.Do(ctx, s.client.B().Hdel().Key(key).Field(matching...).Build()).Error()
	})
}

// DeleteIssuers removes the registrations whose issuer key matches.
func (s *ClientCredentialStore) DeleteIssuers(_ context.Context, match func(string) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for issuer := range s.creds {
		if match(issuer) {
			delete(s.creds, issuer)
		}
	}
	return nil
}

// DeleteIssuers removes the registrations whose issuer key matches.
func (s *ValkeyClientCredentialStore) DeleteIssuers(ctx context.Context, match func(string) bool) error {
	issuers, err := s.client.Do(ctx, s.client.B().Hkeys().Key(s.key()).Build()).AsStrSlice()
	if err != nil {
		return err
	}
	var matching []string
	for _, issuer := range issuers {
		if match(issuer) {
			matching = append(matching, issuer)
		}
	}
	if len(matching) == 0 {
		return nil
	}
	return s.client.Do(ctx, s.client.B().Hdel().Key(s.key()).Field(matching...).Build()).Error()
}

// DeleteIssuers removes the flows in progress whose issuer key matches, so
// their callbacks find no state.
func (ss *StateStore) DeleteIssuers(_ context.Context, match func(string) bool) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for nonce, state := range ss.states {
		if match(state.Issuer) {
			delete(ss.states, nonce)
		}
	}
	return nil
}

// DeleteIssuers removes the flows in progress whose issuer key matches. A
// state that cannot be read is left to its TTL.
func (s *ValkeyStateStore) DeleteIssuers(ctx context.Context, match func(string) bool) error {
	return scanKeys(ctx, s.client, s.keyPrefix+"oauth:state:*", func(key string) error {
		stored, err := s.client.Do(ctx, s.client.B().Get().Key(key).Build()).ToString()
		if err != nil {
			if valkey.IsValkeyNil(err) {
				return nil
			}
			return err
		}
		plaintext, err := s.decryptValue(stored)
		if err != nil {
			logging.Warn("OAuth", "ValkeyStateStore: cannot read a state while forgetting issuers: %v", err)
			return nil
		}
		var entry valkeyStateEntry
		if err := json.Unmarshal(plaintext, &entry); err != nil || !match(entry.Issuer) {
			return nil
		}
		return s.client.Do(ctx, s.client.B().Del().Key(key).Build()).Error()
	})
}

// scanKeys calls visit for every key that matches pattern.
func scanKeys(ctx context.Context, client valkey.Client, pattern string, visit func(key string) error) error {
	var cursor uint64
	for {
		entry, err := client.Do(ctx, client.B().Scan().Cursor(cursor).Match(pattern).Count(forgetScanCount).Build()).AsScanEntry()
		if err != nil {
			return err
		}
		for _, key := range entry.Elements {
			if err := visit(key); err != nil {
				return err
			}
		}
		cursor = entry.Cursor
		if cursor == 0 {
			return nil
		}
	}
}
