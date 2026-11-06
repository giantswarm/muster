package api

import (
	"context"
	"sync"
)

// DelegatedCaller is a caller that presented a delegated token: a token from a
// trusted issuer whose sub names a person and whose act claim names the party
// acting for that person (RFC 8693 §4.1). The token is muster's credential for
// the call only: it is exchanged for backend tokens and never forwarded.
type DelegatedCaller struct {
	// Token is the validated inbound bearer.
	Token string
	// Issuer and Subject are the token's iss and raw sub.
	Issuer  string
	Subject string
	// ActorIssuer and ActorChain name the acting parties, the current actor
	// (outermost act) first.
	ActorIssuer string
	ActorChain  []string
	// Toolset is the selector list the token's scope claim granted.
	Toolset []string
}

type delegatedCallerContextKey struct{}

// WithDelegatedCaller returns a context marking the request as a delegated
// caller's.
func WithDelegatedCaller(ctx context.Context, caller DelegatedCaller) context.Context {
	return context.WithValue(ctx, delegatedCallerContextKey{}, caller)
}

// DelegatedCallerFromContext returns the delegated caller of the request, and
// false when the request is not a delegated caller's.
func DelegatedCallerFromContext(ctx context.Context) (DelegatedCaller, bool) {
	caller, ok := ctx.Value(delegatedCallerContextKey{}).(DelegatedCaller)
	return caller, ok
}

// DelegationExchange resolves the RFC 8693 exchange muster runs for a
// delegated caller at the Dex that issued the caller's token, for a token of
// muster's choosing (the Kubernetes API server's, for instance): the token
// endpoint, muster's Dex client, and muster's workload token as actor. The
// caller sets Delegation.Audience.
type DelegationExchange interface {
	DelegationExchangeConfig(caller DelegatedCaller) (TokenExchangeConfig, error)
}

var (
	delegationExchangeMu sync.RWMutex
	delegationExchange   DelegationExchange
)

// RegisterDelegationExchange registers the DelegationExchange implementation;
// nil unregisters it.
func RegisterDelegationExchange(h DelegationExchange) {
	delegationExchangeMu.Lock()
	defer delegationExchangeMu.Unlock()
	delegationExchange = h
}

// GetDelegationExchange returns the registered DelegationExchange, nil when
// none is.
func GetDelegationExchange() DelegationExchange {
	delegationExchangeMu.RLock()
	defer delegationExchangeMu.RUnlock()
	return delegationExchange
}
