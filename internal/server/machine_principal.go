package server

import (
	"github.com/giantswarm/muster/internal/config"
)

// lookupMachinePrincipal returns the MachinePrincipalConfig for the given
// subject claim, or (zero, false) when the subject is not in the map.
func lookupMachinePrincipal(sub string, principals map[string]config.MachinePrincipalConfig) (config.MachinePrincipalConfig, bool) {
	if len(principals) == 0 {
		return config.MachinePrincipalConfig{}, false
	}
	mp, ok := principals[sub]
	return mp, ok
}
