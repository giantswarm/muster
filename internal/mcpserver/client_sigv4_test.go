package mcpserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

func TestSigV4ServiceFromURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		want      string
		wantError bool
	}{
		{
			name: "the AWS-hosted endpoint signs as aws-mcp",
			url:  "https://aws-mcp.eu-central-1.api.aws/mcp",
			want: "aws-mcp",
		},
		{
			name: "a port is not part of the hostname",
			url:  "http://localhost:9000/mcp",
			want: "localhost",
		},
		{
			name:      "a url without a host is rejected",
			url:       "/mcp",
			wantError: true,
		},
		{
			name:      "an unparsable url is rejected",
			url:       "https://exa mple.com/mcp",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sigV4ServiceFromURL(tt.url)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// transportOf builds the signing client's HTTP client and returns the signing
// transport, unwrapping the metadata transport in front of it when the server
// configures entries. The credential provider resolves from the ambient
// environment, which in a test carries no pod identity, so only the wiring is
// asserted.
func transportOf(t *testing.T, client *StreamableHTTPClient) *sigV4Transport {
	t.Helper()

	require.NotNil(t, client.httpClientFunc)
	httpClient, err := client.httpClientFunc(context.Background())
	require.NoError(t, err)
	assert.Zero(t, httpClient.Timeout, "the continuous-listening GET must not be cut by a client timeout")

	next := httpClient.Transport
	if injecting, wrapped := next.(*metaTransport); wrapped {
		next = injecting.base
	}

	transport, ok := next.(*sigV4Transport)
	require.True(t, ok, "the HTTP client must sign through sigV4Transport")
	return transport
}

// metaTransportOf returns the metadata transport in front of the signer, or
// nil when the client injects nothing. The order is the point: the body has to
// be rewritten before it is signed.
func metaTransportOf(t *testing.T, client *StreamableHTTPClient) *metaTransport {
	t.Helper()

	require.NotNil(t, client.httpClientFunc)
	httpClient, err := client.httpClientFunc(context.Background())
	require.NoError(t, err)

	injecting, wrapped := httpClient.Transport.(*metaTransport)
	if !wrapped {
		return nil
	}
	assert.IsType(t, &sigV4Transport{}, injecting.base, "the signer must see the rewritten body")
	return injecting
}

func TestNewSigV4Client(t *testing.T) {
	t.Run("the service defaults to the first hostname label", func(t *testing.T) {
		client, err := newSigV4Client(testSigV4URL, map[string]string{"X-Trace": "on"},
			api.MCPServerSigV4{Region: testSigV4Region}, map[string]string{"AWS_REGION": "us-east-1"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"X-Trace": "on"}, client.headers)

		transport := transportOf(t, client)
		assert.Equal(t, testSigV4Region, transport.region)
		assert.Equal(t, testSigV4Service, transport.service)

		injecting := metaTransportOf(t, client)
		require.NotNil(t, injecting, "configured entries must reach the transport chain")
		assert.Equal(t, map[string]string{"AWS_REGION": "us-east-1"}, injecting.meta)
	})

	t.Run("an explicit service wins", func(t *testing.T) {
		client, err := newSigV4Client(testSigV4URL, nil,
			api.MCPServerSigV4{Region: testSigV4Region, Service: "execute-api"}, nil)
		require.NoError(t, err)
		assert.Equal(t, "execute-api", transportOf(t, client).service)
		assert.Nil(t, metaTransportOf(t, client), "no entries means no body rewrite")
	})

	t.Run("an underivable service is rejected", func(t *testing.T) {
		_, err := newSigV4Client("/mcp", nil, api.MCPServerSigV4{Region: testSigV4Region}, nil)
		require.ErrorContains(t, err, "cannot derive sigv4 service")
	})
}

func TestNewMCPClientFromTypeSigV4(t *testing.T) {
	sigv4Auth := &api.MCPServerAuth{
		Type:  api.MCPServerAuthTypeSigV4,
		SigV4: &api.MCPServerSigV4{Region: testSigV4Region},
	}

	t.Run("a sigv4 auth type yields a signing client", func(t *testing.T) {
		client, err := NewMCPClientFromType(api.MCPServerTypeStreamableHTTP, MCPClientConfig{
			URL:  testSigV4URL,
			Auth: sigv4Auth,
			Meta: map[string]string{"AWS_REGION": testSigV4Region},
		})
		require.NoError(t, err)

		streamable, ok := client.(*StreamableHTTPClient)
		require.True(t, ok)
		assert.NotNil(t, streamable.httpClientFunc)
	})

	// The factory is the single runtime enforcement point, so the rules
	// api.ValidateSigV4 defines have to bite here.
	t.Run("a sigv4 auth type without a sigv4 block is rejected", func(t *testing.T) {
		_, err := NewMCPClientFromType(api.MCPServerTypeStreamableHTTP, MCPClientConfig{
			URL:  testSigV4URL,
			Auth: &api.MCPServerAuth{Type: api.MCPServerAuthTypeSigV4},
		})
		require.ErrorContains(t, err, "auth.sigv4.region is required")
	})

	t.Run("sigv4 on sse is rejected rather than silently unsigned", func(t *testing.T) {
		_, err := NewMCPClientFromType(api.MCPServerTypeSSE, MCPClientConfig{
			URL:  testSigV4URL,
			Auth: sigv4Auth,
		})
		require.ErrorContains(t, err, "streamable-http")
	})

	t.Run("another auth type leaves the plain client in place", func(t *testing.T) {
		client, err := NewMCPClientFromType(api.MCPServerTypeStreamableHTTP, MCPClientConfig{
			URL:  testSigV4URL,
			Auth: &api.MCPServerAuth{Type: "none"},
		})
		require.NoError(t, err)
		assert.Nil(t, client.(*StreamableHTTPClient).httpClientFunc)
	})
}
