package aggregator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSSOLifecycleOptions_Count pins the three callbacks the aggregator wires
// onto mcp-oauth's token-family lifecycle. A future refactor that drops or
// adds an option will break this test loudly instead of silently disabling
// the SSO setup path.
func TestSSOLifecycleOptions_Count(t *testing.T) {
	t.Parallel()

	a := newTestAggregatorWithPool(t)

	opts := a.ssoLifecycleOptions()

	require.Len(t, opts, 3)
	for i, opt := range opts {
		require.NotNil(t, opt, "option %d is nil", i)
	}
}
