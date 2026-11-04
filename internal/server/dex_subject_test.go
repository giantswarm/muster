package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Dex's sub is its IDTokenSubject message, user_id (1) and conn_id (2),
// protobuf-encoded and base64url without padding.
func TestDexSubjectConnector(t *testing.T) {
	for name, tc := range map[string]struct {
		subject string
		want    string
		ok      bool
	}{
		"github connector":  {subject: "CgVhbGljZRIGZ2l0aHVi", want: "github", ok: true},
		"azure-ad":          {subject: "CgVhbGljZRIIYXp1cmUtYWQ", want: "azure-ad", ok: true},
		"no connector":      {subject: "CgVhbGljZQ"},
		"not base64url":     {subject: "alice@example.com"},
		"not protobuf":      {subject: "YWxpY2U"},
		"empty":             {subject: ""},
		"truncated message": {subject: "CgVhbGlj"},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := dexSubjectConnector(tc.subject)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}
