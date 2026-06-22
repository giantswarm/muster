package api

import (
	"context"
	"sync"
	"time"
)

// BackendTokenMinter mints a per-backend token for the aggregator's localMint
// downstream auth mode. The aggregator calls it at request time with the
// caller's raw subject token and, for delegation, the actor token; the
// implementation runs them through muster's RFC 8693 broker, which validates
// both tokens and enforces ActorDelegationPolicy and WorkloadAudiences before a
// token is minted. Implemented by the OAuth server adapter and registered at
// bootstrap; nil when muster's OAuth server is not running in JWT mode.
type BackendTokenMinter interface {
	MintBackendToken(ctx context.Context, req BackendMintRequest) (BackendMintResult, error)
}

// BackendMintRequest carries the raw RFC 8693 inputs of a localMint exchange.
// The minter validates the tokens and lifts identity claims (email, groups, act)
// from them; the caller supplies no pre-extracted claims.
type BackendMintRequest struct {
	// SubjectToken is the raw subject token: the human's bearer on the delegation
	// path, or the workload's own token on the M2M path.
	SubjectToken string
	// SubjectTokenType is the RFC 8693 token-type URN of SubjectToken.
	SubjectTokenType string
	// ActorToken is the raw actor token (the agent's workload token) on the
	// delegation path. Empty selects the M2M path (no act claim).
	ActorToken string
	// ActorTokenType is the RFC 8693 token-type URN of ActorToken. Empty when
	// ActorToken is empty.
	ActorTokenType string
	// Audience is the minted token's aud: the backend resource identifier, which
	// must match a configured broker local-mint target.
	Audience string
	// Resource is the optional RFC 8707 resource parameter.
	Resource string
	// Scope is the optional requested scope.
	Scope string
}

// BackendMintResult is the minted token and its expiry.
type BackendMintResult struct {
	AccessToken string
	ExpiresAt   time.Time
}

var (
	backendTokenMinter      BackendTokenMinter
	backendTokenMinterMutex sync.RWMutex
)

// RegisterBackendTokenMinter registers the backend token minter implementation.
// Thread-safe; call during system initialization. A nil minter clears the
// registration.
func RegisterBackendTokenMinter(m BackendTokenMinter) {
	backendTokenMinterMutex.Lock()
	defer backendTokenMinterMutex.Unlock()
	backendTokenMinter = m
}

// GetBackendTokenMinter returns the registered backend token minter, or nil when
// none is registered (OAuth server absent or not in JWT mode). Callers must
// fail closed on nil.
func GetBackendTokenMinter() BackendTokenMinter {
	backendTokenMinterMutex.RLock()
	defer backendTokenMinterMutex.RUnlock()
	return backendTokenMinter
}
