package aggregator

import (
	"context"
	"errors"
)

// mockTokenBroker is a test fake for the TokenBroker port. Unset
// method-override fields return errMockNotConfigured.
type mockTokenBroker struct {
	getTokenFn      func(ctx context.Context, sessionID, audience string) (Token, error)
	sessionIssuerFn func(ctx context.Context, sessionID string) (string, error)
}

var errMockNotConfigured = errors.New("mockTokenBroker: method not configured")

func (m *mockTokenBroker) GetToken(ctx context.Context, sessionID, audience string) (Token, error) {
	if m.getTokenFn != nil {
		return m.getTokenFn(ctx, sessionID, audience)
	}
	return Token{}, errMockNotConfigured
}

func (m *mockTokenBroker) SessionIssuer(ctx context.Context, sessionID string) (string, error) {
	if m.sessionIssuerFn != nil {
		return m.sessionIssuerFn(ctx, sessionID)
	}
	return "", errMockNotConfigured
}

var _ TokenBroker = (*mockTokenBroker)(nil)
