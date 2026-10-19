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
// session hash: one HKEYS round trip and one HDEL round trip per SCAN page.
// The user index keeps its session IDs: it is a reverse lookup only, and its
// entries expire with the sessions.
func (s *ValkeyTokenStore) DeleteIssuers(ctx context.Context, match func(string) bool) error {
	userPrefix := s.keyPrefix + "oauth:token:user:"
	return scanPages(ctx, s.client, s.keyPrefix+"oauth:token:*", func(page []string) error {
		var keys []string
		cmds := make(valkey.Commands, 0, len(page))
		for _, key := range page {
			if strings.HasPrefix(key, userPrefix) {
				continue
			}
			keys = append(keys, key)
			cmds = append(cmds, s.client.B().Hkeys().Key(key).Build())
		}
		var deletes valkey.Commands
		for i, result := range s.client.DoMulti(ctx, cmds...) {
			fields, err := result.AsStrSlice()
			if err != nil {
				return err
			}
			var matching []string
			for _, field := range fields {
				if issuer, _ := parseFieldName(field); match(issuer) {
					matching = append(matching, field)
				}
			}
			if len(matching) > 0 {
				deletes = append(deletes, s.client.B().Hdel().Key(keys[i]).Field(matching...).Build())
			}
		}
		return doAll(ctx, s.client, deletes)
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

// DeleteIssuers removes the flows in progress whose issuer key matches: one
// GET round trip and one DEL round trip per SCAN page. A state that cannot
// be read is left to its TTL.
func (s *ValkeyStateStore) DeleteIssuers(ctx context.Context, match func(string) bool) error {
	return scanPages(ctx, s.client, s.keyPrefix+"oauth:state:*", func(page []string) error {
		cmds := make(valkey.Commands, 0, len(page))
		for _, key := range page {
			cmds = append(cmds, s.client.B().Get().Key(key).Build())
		}
		var deletes valkey.Commands
		for i, result := range s.client.DoMulti(ctx, cmds...) {
			stored, err := result.ToString()
			if err != nil {
				if valkey.IsValkeyNil(err) {
					continue
				}
				return err
			}
			plaintext, err := s.decryptValue(stored)
			if err != nil {
				logging.Warn("OAuth", "ValkeyStateStore: cannot read a state while forgetting issuers: %v", err)
				continue
			}
			var entry valkeyStateEntry
			if err := json.Unmarshal(plaintext, &entry); err != nil || !match(entry.Issuer) {
				continue
			}
			deletes = append(deletes, s.client.B().Del().Key(page[i]).Build())
		}
		return doAll(ctx, s.client, deletes)
	})
}

// doAll sends cmds in one round trip and returns the first error.
func doAll(ctx context.Context, client valkey.Client, cmds valkey.Commands) error {
	if len(cmds) == 0 {
		return nil
	}
	for _, result := range client.DoMulti(ctx, cmds...) {
		if err := result.Error(); err != nil {
			return err
		}
	}
	return nil
}

// scanPages calls visit with every SCAN page of the keys that match pattern.
func scanPages(ctx context.Context, client valkey.Client, pattern string, visit func(page []string) error) error {
	var cursor uint64
	for {
		entry, err := client.Do(ctx, client.B().Scan().Cursor(cursor).Match(pattern).Count(forgetScanCount).Build()).AsScanEntry()
		if err != nil {
			return err
		}
		if len(entry.Elements) > 0 {
			if err := visit(entry.Elements); err != nil {
				return err
			}
		}
		cursor = entry.Cursor
		if cursor == 0 {
			return nil
		}
	}
}
