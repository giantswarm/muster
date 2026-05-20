package agentgateway_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
)

func TestHTTPTarget_Validate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{name: "zero", port: 0, wantErr: true},
		{name: "negative", port: -1, wantErr: true},
		{name: "one", port: 1, wantErr: false},
		{name: "max", port: 65535, wantErr: false},
		{name: "overflow", port: 65536, wantErr: true},
		{name: "common", port: 8080, wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := agentgateway.HTTPTarget{
				Protocol: agentgateway.StreamableHTTP,
				Host:     "example.invalid",
				Port:     tc.port,
				Path:     "/mcp",
			}
			err := target.Validate()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestAuthn_RequiresPolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		typ          agentgateway.AuthnType
		forwardToken bool
		want         bool
	}{
		{name: "none/no-forward", typ: agentgateway.AuthnTypeNone, forwardToken: false, want: false},
		{name: "none/forward", typ: agentgateway.AuthnTypeNone, forwardToken: true, want: true},
		{name: "oauth/no-forward", typ: agentgateway.AuthnTypeOAuth, forwardToken: false, want: true},
		{name: "oauth/forward", typ: agentgateway.AuthnTypeOAuth, forwardToken: true, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			authn := agentgateway.Authn{Type: tc.typ, ForwardToken: tc.forwardToken}
			require.Equal(t, tc.want, authn.RequiresPolicy())
		})
	}
}
