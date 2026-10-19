package store

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

// Two writers that add different issuer keys to one server's record (two
// replicas, a login and an adoption) lose neither, and removing a server
// removes all of its record and nothing of another server's.
func TestTenantOwnerStore_AddsKeysWithoutOverwriting(t *testing.T) {
	srv := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	stores := map[string]TenantOwnerStore{
		"memory": NewInMemoryTenantOwnerStore(),
		"valkey": NewValkeyTenantOwnerStore(client, "muster:"),
	}
	for name, s := range stores {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			require.NoError(t, s.AddOwner(ctx, "org-a", "uid-g", TenantOwner{Server: "org-a/github", IssuerKeys: []string{"ns:org-a:https://a"}}))
			require.NoError(t, s.AddOwner(ctx, "org-a", "uid-g", TenantOwner{Server: "org-a/github", IssuerKeys: []string{"ns:org-a:https://b"}}))
			require.NoError(t, s.AddOwner(ctx, "org-a", "uid-m", TenantOwner{Server: "org-a/miro"}))

			owners, err := s.Owners(ctx, "org-a")
			require.NoError(t, err)
			assert.Equal(t, "org-a/github", owners["uid-g"].Server)
			assert.ElementsMatch(t, []string{"ns:org-a:https://a", "ns:org-a:https://b"}, owners["uid-g"].IssuerKeys)

			require.NoError(t, s.RemoveOwners(ctx, "org-a", "uid-g"))
			owners, err = s.Owners(ctx, "org-a")
			require.NoError(t, err)
			assert.Equal(t, map[string]TenantOwner{"uid-m": {Server: "org-a/miro"}}, owners)
		})
	}
}
