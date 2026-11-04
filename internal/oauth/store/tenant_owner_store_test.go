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

// The store lists the namespaces that hold a record, and a namespace leaves
// the list when its last server is removed: the sweep at start reads the
// list after a restart (giantswarm/muster#1772).
func TestTenantOwnerStore_ListsTheNamespacesWithARecord(t *testing.T) {
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
			namespaces, err := s.Namespaces(ctx)
			require.NoError(t, err)
			assert.Empty(t, namespaces)

			require.NoError(t, s.AddOwner(ctx, "org-a", "uid-g", TenantOwner{Server: "org-a/github", IssuerKeys: []string{"ns:org-a:https://a"}}))
			require.NoError(t, s.AddOwner(ctx, "org-b", "uid-f", TenantOwner{Server: "org-b/fixture"}))
			require.NoError(t, s.AddOwner(ctx, "org-b", "uid-m", TenantOwner{Server: "org-b/miro"}))
			namespaces, err = s.Namespaces(ctx)
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"org-a", "org-b"}, namespaces)

			require.NoError(t, s.RemoveOwners(ctx, "org-b", "uid-f"))
			namespaces, err = s.Namespaces(ctx)
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"org-a", "org-b"}, namespaces, "org-b keeps a server")

			require.NoError(t, s.RemoveOwners(ctx, "org-b", "uid-m"))
			require.NoError(t, s.RemoveOwners(ctx, "org-a", "uid-g"))
			namespaces, err = s.Namespaces(ctx)
			require.NoError(t, err)
			assert.Empty(t, namespaces)
		})
	}
}

// Records a muster without the namespace index wrote are listed after an
// upgrade: the first listing finds them once, by their keys.
func TestTenantOwnerStore_ListsRecordsWrittenBeforeTheIndex(t *testing.T) {
	srv := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	ctx := context.Background()
	// The layout of the records, as the earlier muster wrote them.
	srv.HSet("muster:tenant_owners:org-old", "uid-o", "org-old/github")
	srv.HSet("muster:tenant_owners:org-old", "uid-o/key/ns:org-old:https://a", "")
	srv.HSet("other:tenant_owners:org-x", "uid-x", "org-x/github")

	s := NewValkeyTenantOwnerStore(client, "muster:")
	require.NoError(t, s.AddOwner(ctx, "org-new", "uid-n", TenantOwner{Server: "org-new/github"}))
	namespaces, err := s.Namespaces(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"org-old", "org-new"}, namespaces)

	require.NoError(t, s.RemoveOwners(ctx, "org-old", "uid-o"))
	namespaces, err = s.Namespaces(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"org-new"}, namespaces, "an earlier record removed after the upgrade leaves the list")
}
