package callerwrite

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

func TestReloginErrorIdentifiesCallerAuthentication(t *testing.T) {
	err := ReloginError("The Kubernetes API rejected your session token.")
	var authentication *api.CallerAuthenticationError
	require.ErrorAs(t, err, &authentication)
	require.EqualError(t, err, "The Kubernetes API rejected your session token. A re-login is required: sign in again to get a fresh session token, then retry.")
}
