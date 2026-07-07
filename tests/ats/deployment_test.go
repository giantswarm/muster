//go:build smoke || upgrade

package ats

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/tests/assertions"
)

const deploymentReadyTimeout = 3 * time.Minute

func TestDeploymentReady(t *testing.T) {
	err := assertions.DeploymentReady(t.Context(), newClusterClient(t), newTarget(t), deploymentReadyTimeout)
	require.NoError(t, err)
}
