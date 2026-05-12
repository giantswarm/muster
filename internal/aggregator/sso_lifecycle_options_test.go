package aggregator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSSOLifecycleOptions_WiresThreeNonNilCallbacks pins the three callbacks the
// aggregator wires onto mcp-oauth's token-family lifecycle.
//
// This is a count pin, not a kind pin: oauth.ServerOption is opaque, so we
// cannot introspect which handler each entry installs. A refactor that swaps
// (e.g.) WithSessionRevocationHandler for a second WithSessionCreationHandler
// keeps the count at three and would slip past this test. Drop one, add a
// fourth, or return nil and the test trips.
func TestSSOLifecycleOptions_WiresThreeNonNilCallbacks(t *testing.T) {
	t.Parallel()

	a := newTestAggregatorWithPool(t)

	opts := a.ssoLifecycleOptions()

	require.Len(t, opts, 3)
	for i, opt := range opts {
		require.NotNil(t, opt, "option %d is nil", i)
	}
}
