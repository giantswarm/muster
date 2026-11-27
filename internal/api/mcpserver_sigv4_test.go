package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testSigV4Region  = "eu-central-1"
	testSigV4RoleARN = "arn:aws:iam::123456789012:role/ExampleReadOnlyRole"
)

func TestValidateSigV4(t *testing.T) {
	sigv4 := &MCPServerSigV4{Region: testSigV4Region}

	tests := []struct {
		name       string
		serverType MCPServerType
		auth       *MCPServerAuth
		wantError  string
	}{
		{
			name:       "no auth block",
			serverType: MCPServerTypeStreamableHTTP,
		},
		{
			name:       "sigv4 on streamable-http",
			serverType: MCPServerTypeStreamableHTTP,
			auth:       &MCPServerAuth{Type: MCPServerAuthTypeSigV4, SigV4: sigv4},
		},
		{
			name:       "sigv4 on sse",
			serverType: MCPServerTypeSSE,
			auth:       &MCPServerAuth{Type: MCPServerAuthTypeSigV4, SigV4: sigv4},
			wantError:  `is only allowed when type is "streamable-http"`,
		},
		{
			name:       "sigv4 on stdio",
			serverType: MCPServerTypeStdio,
			auth:       &MCPServerAuth{Type: MCPServerAuthTypeSigV4, SigV4: sigv4},
			wantError:  `is only allowed when type is "streamable-http"`,
		},
		{
			name:       "sigv4 type without the block",
			serverType: MCPServerTypeStreamableHTTP,
			auth:       &MCPServerAuth{Type: MCPServerAuthTypeSigV4},
			wantError:  "auth.sigv4.region is required",
		},
		{
			name:       "sigv4 block without a region",
			serverType: MCPServerTypeStreamableHTTP,
			auth:       &MCPServerAuth{Type: MCPServerAuthTypeSigV4, SigV4: &MCPServerSigV4{}},
			wantError:  "auth.sigv4.region is required",
		},
		{
			name:       "sigv4 block under another auth type",
			serverType: MCPServerTypeStreamableHTTP,
			auth:       &MCPServerAuth{Type: "oauth", SigV4: sigv4},
			wantError:  `auth.sigv4 is only allowed when auth.type is "sigv4"`,
		},
		{
			name:       "sigv4 with forwardToken",
			serverType: MCPServerTypeStreamableHTTP,
			auth: &MCPServerAuth{
				Type: MCPServerAuthTypeSigV4, SigV4: sigv4, ForwardToken: true,
			},
			wantError: "auth.forwardToken does not apply",
		},
		{
			name:       "sigv4 with tokenExchange",
			serverType: MCPServerTypeStreamableHTTP,
			auth: &MCPServerAuth{
				Type: MCPServerAuthTypeSigV4, SigV4: sigv4,
				TokenExchange: &TokenExchangeConfig{Enabled: true},
			},
			wantError: "auth.tokenExchange does not apply",
		},
		{
			name:       "caller source identity with a role",
			serverType: MCPServerTypeStreamableHTTP,
			auth: &MCPServerAuth{Type: MCPServerAuthTypeSigV4, SigV4: &MCPServerSigV4{
				Region: testSigV4Region, RoleARN: testSigV4RoleARN, SourceIdentity: SigV4SourceIdentityCaller,
			}},
		},
		{
			name:       "caller source identity without a role",
			serverType: MCPServerTypeStreamableHTTP,
			auth: &MCPServerAuth{Type: MCPServerAuthTypeSigV4, SigV4: &MCPServerSigV4{
				Region: testSigV4Region, SourceIdentity: SigV4SourceIdentityCaller,
			}},
			wantError: "auth.sigv4.sourceIdentity needs auth.sigv4.roleArn",
		},
		{
			name:       "unknown source identity",
			serverType: MCPServerTypeStreamableHTTP,
			auth: &MCPServerAuth{Type: MCPServerAuthTypeSigV4, SigV4: &MCPServerSigV4{
				Region: testSigV4Region, RoleARN: testSigV4RoleARN, SourceIdentity: "email",
			}},
			wantError: `auth.sigv4.sourceIdentity must be "caller"`,
		},
		{
			name:       "oauth is untouched",
			serverType: MCPServerTypeStreamableHTTP,
			auth:       &MCPServerAuth{Type: "oauth", ForwardToken: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSigV4(string(tt.serverType), tt.auth)
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}
