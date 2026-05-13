package aggregator

import (
	"context"
	"errors"
)

// mockTokenBroker is a test fake for the TokenBroker port. Unset
// method-override fields return errMockNotConfigured.
type mockTokenBroker struct {
	enabled           bool
	beginOAuthFlowFn  func(ctx context.Context, req BeginRequest) (FlowURL, error)
	getTokenFn        func(ctx context.Context, sessionID, audience string) (Token, error)
	exchangeTokenFn   func(ctx context.Context, req ExchangeRequest) (Token, error)
	invalidateTokenFn func(ctx context.Context, sessionID, audience string) error
	sessionIssuerFn   func(ctx context.Context, sessionID string) (string, error)
}

var errMockNotConfigured = errors.New("mockTokenBroker: method not configured")

func (m *mockTokenBroker) Enabled() bool { return m.enabled }

func (m *mockTokenBroker) BeginOAuthFlow(ctx context.Context, req BeginRequest) (FlowURL, error) {
	if m.beginOAuthFlowFn != nil {
		return m.beginOAuthFlowFn(ctx, req)
	}
	return FlowURL{}, errMockNotConfigured
}

func (m *mockTokenBroker) GetToken(ctx context.Context, sessionID, audience string) (Token, error) {
	if m.getTokenFn != nil {
		return m.getTokenFn(ctx, sessionID, audience)
	}
	return Token{}, errMockNotConfigured
}

func (m *mockTokenBroker) ExchangeToken(ctx context.Context, req ExchangeRequest) (Token, error) {
	if m.exchangeTokenFn != nil {
		return m.exchangeTokenFn(ctx, req)
	}
	return Token{}, errMockNotConfigured
}

func (m *mockTokenBroker) InvalidateToken(ctx context.Context, sessionID, audience string) error {
	if m.invalidateTokenFn != nil {
		return m.invalidateTokenFn(ctx, sessionID, audience)
	}
	return errMockNotConfigured
}

func (m *mockTokenBroker) SessionIssuer(ctx context.Context, sessionID string) (string, error) {
	if m.sessionIssuerFn != nil {
		return m.sessionIssuerFn(ctx, sessionID)
	}
	return "", errMockNotConfigured
}

var _ TokenBroker = (*mockTokenBroker)(nil)
