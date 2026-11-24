package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateRequestMeta(t *testing.T) {
	region := map[string]string{AWSRegionRequestMetaKey: "eu-central-1"}
	sigv4 := &MCPServerAuth{Type: MCPServerAuthTypeSigV4, SigV4: &MCPServerSigV4{Region: "eu-central-1"}}

	tests := []struct {
		name        string
		serverType  MCPServerType
		auth        *MCPServerAuth
		requestMeta map[string]string
		wantError   string
	}{
		{name: "streamable-http", serverType: MCPServerTypeStreamableHTTP, requestMeta: region},
		{name: "sse", serverType: MCPServerTypeSSE, requestMeta: region},
		{name: "nothing on stdio", serverType: MCPServerTypeStdio},
		{name: "an empty map on stdio", serverType: MCPServerTypeStdio, requestMeta: map[string]string{}},
		{
			name:        "stdio has no HTTP transport to add the entries",
			serverType:  MCPServerTypeStdio,
			requestMeta: region,
			wantError:   "requestMeta is only allowed",
		},
		{
			name:        "prefixed key",
			serverType:  MCPServerTypeStreamableHTTP,
			requestMeta: map[string]string{"com.example/tenant-id": "a"},
		},
		{
			name:        "name with dots, dashes and underscores",
			serverType:  MCPServerTypeStreamableHTTP,
			requestMeta: map[string]string{"a.b-c_d": "a"},
		},
		{
			name:        "name ending in a separator",
			serverType:  MCPServerTypeStreamableHTTP,
			requestMeta: map[string]string{"region_": "a"},
			wantError:   `"region_" does not follow the MCP _meta key format`,
		},
		{
			name:        "prefix without a name",
			serverType:  MCPServerTypeStreamableHTTP,
			requestMeta: map[string]string{"com.example/": "a"},
			wantError:   "does not follow the MCP _meta key format",
		},
		{
			name:        "prefix label starting with a digit",
			serverType:  MCPServerTypeStreamableHTTP,
			requestMeta: map[string]string{"1com.example/x": "a"},
			wantError:   "does not follow the MCP _meta key format",
		},
		{
			name:        "MCP reserved prefix",
			serverType:  MCPServerTypeStreamableHTTP,
			requestMeta: map[string]string{"io.modelcontextprotocol/clientInfo": "a"},
			wantError:   "reserved for MCP",
		},
		{
			name:        "MCP reserved short prefix",
			serverType:  MCPServerTypeStreamableHTTP,
			requestMeta: map[string]string{"dev.mcp.tools/x": "a"},
			wantError:   "reserved for MCP",
		},
		{
			name:        "mcp as first label is not reserved",
			serverType:  MCPServerTypeStreamableHTTP,
			requestMeta: map[string]string{"mcp.example/x": "a"},
		},
		{
			name:        "progressToken belongs to muster",
			serverType:  MCPServerTypeStreamableHTTP,
			requestMeta: map[string]string{"progressToken": "a"},
			wantError:   "set by muster or OpenTelemetry",
		},
		{
			name:        "traceparent belongs to OpenTelemetry",
			serverType:  MCPServerTypeStreamableHTTP,
			requestMeta: map[string]string{"traceparent": "a"},
			wantError:   "set by muster or OpenTelemetry",
		},
		{
			name:        "every problem is reported",
			serverType:  MCPServerTypeStreamableHTTP,
			requestMeta: map[string]string{"traceparent": "a", "baggage": "b"},
			wantError:   `"baggage" is set by muster or OpenTelemetry, not by configuration; "traceparent" is set by muster or OpenTelemetry`,
		},
		{name: "sigv4 with a region", serverType: MCPServerTypeStreamableHTTP, auth: sigv4, requestMeta: region},
		{
			name:       "sigv4 without requestMeta",
			serverType: MCPServerTypeStreamableHTTP,
			auth:       sigv4,
			wantError:  "needs requestMeta.AWS_REGION",
		},
		{
			name:        "sigv4 with an empty region",
			serverType:  MCPServerTypeStreamableHTTP,
			auth:        sigv4,
			requestMeta: map[string]string{AWSRegionRequestMetaKey: ""},
			wantError:   "needs requestMeta.AWS_REGION",
		},
		{
			name:        "AWS_REGION is not required without sigv4",
			serverType:  MCPServerTypeStreamableHTTP,
			auth:        &MCPServerAuth{Type: MCPServerAuthTypeOAuth},
			requestMeta: map[string]string{"com.example/x": "a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRequestMeta(string(tt.serverType), tt.auth, tt.requestMeta)
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestResolveRequestMeta(t *testing.T) {
	current := map[string]string{"a": "1"}
	deprecated := map[string]string{"b": "2"}

	got, err := ResolveRequestMeta(current, nil)
	require.NoError(t, err)
	require.Equal(t, current, got)

	got, err = ResolveRequestMeta(nil, deprecated)
	require.NoError(t, err)
	require.Equal(t, deprecated, got)

	got, err = ResolveRequestMeta(nil, nil)
	require.NoError(t, err)
	require.Nil(t, got)

	_, err = ResolveRequestMeta(current, deprecated)
	require.ErrorContains(t, err, "set requestMeta only")
}
