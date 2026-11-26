package aggregator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsLoopbackListener(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost":   true,
		"127.0.0.1":   true,
		"127.0.0.2":   true,
		"::1":         true,
		"":            false,
		"0.0.0.0":     false,
		"::":          false,
		"10.0.0.5":    false,
		"muster.home": false,
	} {
		require.Equal(t, want, isLoopbackListener(host), host)
	}
}
