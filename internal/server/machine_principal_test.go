package server

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/config"
)

func TestExtractJWTSub(t *testing.T) {
	tests := []struct {
		name    string
		rawJWT  string
		wantSub string
	}{
		{
			// header.{"sub":"system:serviceaccount:ns:sa"}.sig
			name:    "valid JWT with sub",
			rawJWT:  "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJzeXN0ZW06c2VydmljZWFjY291bnQ6bnM6c2EiLCJpc3MiOiJodHRwczovL2V4YW1wbGUuY29tIn0.sig",
			wantSub: "system:serviceaccount:ns:sa",
		},
		{
			name:    "empty string",
			rawJWT:  "",
			wantSub: "",
		},
		{
			name:    "not a JWT",
			rawJWT:  "notajwt",
			wantSub: "",
		},
		{
			name:    "JWT with no sub claim",
			rawJWT:  "eyJhbGciOiJSUzI1NiJ9.eyJpc3MiOiJodHRwczovL2V4YW1wbGUuY29tIn0.sig",
			wantSub: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantSub, extractJWTSub(tc.rawJWT))
		})
	}
}

func TestLookupMachinePrincipal(t *testing.T) {
	principals := map[string]config.MachinePrincipalConfig{
		"system:serviceaccount:muster-m2m-test:klaus-sre": {
			Email:  "klaus-sre@machine.giantswarm.io",
			Groups: []string{"klaus-sre"},
		},
		"system:serviceaccount:ai-platform:observability-agent": {
			Email: "observability-agent@machine.giantswarm.io",
		},
	}

	tests := []struct {
		name       string
		sub        string
		principals map[string]config.MachinePrincipalConfig
		wantEmail  string
		wantGroups []string
		wantFound  bool
	}{
		{
			name:       "known sub with groups",
			sub:        "system:serviceaccount:muster-m2m-test:klaus-sre",
			principals: principals,
			wantEmail:  "klaus-sre@machine.giantswarm.io",
			wantGroups: []string{"klaus-sre"},
			wantFound:  true,
		},
		{
			name:       "known sub without groups",
			sub:        "system:serviceaccount:ai-platform:observability-agent",
			principals: principals,
			wantEmail:  "observability-agent@machine.giantswarm.io",
			wantGroups: nil,
			wantFound:  true,
		},
		{
			name:       "unknown sub",
			sub:        "system:serviceaccount:default:other",
			principals: principals,
			wantFound:  false,
		},
		{
			name:       "nil map",
			sub:        "system:serviceaccount:muster-m2m-test:klaus-sre",
			principals: nil,
			wantFound:  false,
		},
		{
			name:       "empty map",
			sub:        "system:serviceaccount:muster-m2m-test:klaus-sre",
			principals: map[string]config.MachinePrincipalConfig{},
			wantFound:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := lookupMachinePrincipal(tc.sub, tc.principals)
			require.Equal(t, tc.wantFound, ok)
			if tc.wantFound {
				require.Equal(t, tc.wantEmail, got.Email)
				require.Equal(t, tc.wantGroups, got.Groups)
			}
		})
	}
}
