package translator_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/reconciler/translator"
)

// TestShimImage_RegistryAndRepository documents the contract callers depend
// on so a sloppy rename here is caught immediately.
func TestShimImage_RegistryAndRepository(t *testing.T) {
	t.Parallel()

	require.True(t, strings.HasPrefix(translator.ShimImage, "gsoci.azurecr.io/giantswarm/musterstdio"),
		"shim image must live in the giantswarm registry under the musterstdio repo, got %q", translator.ShimImage)
	require.Contains(t, translator.ShimImage, ":", "shim image must be tagged, got %q", translator.ShimImage)
}
