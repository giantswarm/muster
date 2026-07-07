//go:build smoke

package ats

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/tests/assertions"
)

func TestClusterReachable(t *testing.T) {
	require.NoError(t, assertions.ClusterReachable(t.Context(), newClusterClient(t)))
}
