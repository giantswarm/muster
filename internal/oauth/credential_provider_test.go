package oauth

import (
	"context"
	"errors"
	"testing"

	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/config"
)

// stubCredentialProvider is a CredentialProvider test double that records calls.
type stubCredentialProvider struct {
	result      *MintResult
	err         error
	mintCalled  bool
	lastMintReq MintRequest
}

func (s *stubCredentialProvider) Mint(_ context.Context, req MintRequest) (*MintResult, error) {
	s.mintCalled = true
	s.lastMintReq = req
	return s.result, s.err
}

// TestProviderRegistry_Dispatch verifies that Exchange selects the registered
// provider and threads the correct MintRequest fields.
func TestProviderRegistry_Dispatch(t *testing.T) {
	stub := &stubCredentialProvider{
		result: &MintResult{AccessToken: "minted-token", IssuedTokenType: "urn:ietf:params:oauth:token-type:access_token"},
	}

	reg := &providerRegistry{
		factories: map[string]providerFactory{
			"stub-provider": func(_ config.BrokerTargetConfig, _ *TokenExchanger, _ string) CredentialProvider {
				return stub
			},
		},
	}

	broker := &BrokerExchanger{
		cfg: config.TokenExchangeBrokerConfig{
			Targets: map[string]config.BrokerTargetConfig{
				"cluster-a": {Type: "stub-provider"},
			},
		},
		registry: reg,
	}

	result, err := broker.Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:         "cluster-a",
		Subject:          &oauthserver.SubjectIdentity{Subject: "user-1"},
		SubjectToken:     "subject-token",
		SubjectTokenType: "urn:ietf:params:oauth:token-type:id_token",
	})
	require.NoError(t, err)

	// Stub was invoked with the correctly threaded MintRequest.
	require.True(t, stub.mintCalled)
	assert.Equal(t, "user-1", stub.lastMintReq.Subject)
	assert.Equal(t, "subject-token", stub.lastMintReq.SubjectToken)
	assert.Equal(t, "urn:ietf:params:oauth:token-type:id_token", stub.lastMintReq.SubjectTokenType)
	assert.Equal(t, "cluster-a", stub.lastMintReq.Target)

	// Result is mapped through to the ExchangerResult.
	assert.Equal(t, "minted-token", result.AccessToken)
	assert.Equal(t, "urn:ietf:params:oauth:token-type:access_token", result.IssuedTokenType)
}

// TestProviderRegistry_UnknownType verifies that a target with an unregistered
// type returns an error wrapping ErrInvalidTarget.
func TestProviderRegistry_UnknownType(t *testing.T) {
	broker := &BrokerExchanger{
		cfg: config.TokenExchangeBrokerConfig{
			Targets: map[string]config.BrokerTargetConfig{
				"cluster-a": {Type: "mystery-provider"},
			},
		},
	}

	_, err := broker.Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:     "cluster-a",
		Subject:      &oauthserver.SubjectIdentity{Subject: "user-1"},
		SubjectToken: "subject-token",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, oauthserver.ErrInvalidTarget),
		"unknown provider type must wrap ErrInvalidTarget, got: %v", err)
}
