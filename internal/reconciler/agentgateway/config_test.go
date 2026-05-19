package agentgateway_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
)

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
