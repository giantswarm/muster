package store

import (
	"context"
	"sync"

	"github.com/valkey-io/valkey-go"
)

// TenantOwnerStore records, per organization namespace, the MCPServer
// objects (by Kubernetes UID, with their server key) whose sign-ins muster
// keeps. A namespace's stored OAuth state belongs to the servers recorded
// here: a server whose UID is not recorded, in a namespace whose recorded
// servers are all gone, is a server of a new namespace of the same name,
// and the state is forgotten before it registers (the aggregator's
// adoptTenantServer). The record survives a restart wherever the tokens do
// (Valkey), so a namespace deleted and created again while muster was down
// is told apart as well.
type TenantOwnerStore interface {
	// Owners returns the recorded servers of the namespace: UID -> server key.
	Owners(ctx context.Context, namespace string) (map[string]string, error)
	// AddOwner records a server of the namespace.
	AddOwner(ctx context.Context, namespace, uid, serverKey string) error
	// RemoveOwners forgets servers of the namespace.
	RemoveOwners(ctx context.Context, namespace string, uids ...string) error
	// DeleteNamespace forgets every server of the namespace.
	DeleteNamespace(ctx context.Context, namespace string) error
}

// InMemoryTenantOwnerStore is the TenantOwnerStore of a muster without
// Valkey; it dies with the process, as the tokens it guards do.
type InMemoryTenantOwnerStore struct {
	mu     sync.Mutex
	owners map[string]map[string]string
}

// NewInMemoryTenantOwnerStore returns an empty in-memory store.
func NewInMemoryTenantOwnerStore() *InMemoryTenantOwnerStore {
	return &InMemoryTenantOwnerStore{owners: make(map[string]map[string]string)}
}

// Owners implements TenantOwnerStore.
func (s *InMemoryTenantOwnerStore) Owners(_ context.Context, namespace string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	owners := make(map[string]string, len(s.owners[namespace]))
	for uid, key := range s.owners[namespace] {
		owners[uid] = key
	}
	return owners, nil
}

// AddOwner implements TenantOwnerStore.
func (s *InMemoryTenantOwnerStore) AddOwner(_ context.Context, namespace, uid, serverKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners[namespace] == nil {
		s.owners[namespace] = make(map[string]string)
	}
	s.owners[namespace][uid] = serverKey
	return nil
}

// RemoveOwners implements TenantOwnerStore.
func (s *InMemoryTenantOwnerStore) RemoveOwners(_ context.Context, namespace string, uids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, uid := range uids {
		delete(s.owners[namespace], uid)
	}
	if len(s.owners[namespace]) == 0 {
		delete(s.owners, namespace)
	}
	return nil
}

// DeleteNamespace implements TenantOwnerStore.
func (s *InMemoryTenantOwnerStore) DeleteNamespace(_ context.Context, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.owners, namespace)
	return nil
}

// ValkeyTenantOwnerStore keeps the records in Valkey, one hash per
// namespace, with no TTL: a record is small and lives as long as its
// namespace's servers.
//
//	Key:    {keyPrefix}tenant_owners:{namespace}
//	Fields: {uid} -> {server key}
type ValkeyTenantOwnerStore struct {
	client    valkey.Client
	keyPrefix string
}

// NewValkeyTenantOwnerStore returns a store on the shared Valkey client.
func NewValkeyTenantOwnerStore(client valkey.Client, keyPrefix string) *ValkeyTenantOwnerStore {
	return &ValkeyTenantOwnerStore{client: client, keyPrefix: keyPrefix}
}

func (s *ValkeyTenantOwnerStore) key(namespace string) string {
	return s.keyPrefix + "tenant_owners:" + namespace
}

// Owners implements TenantOwnerStore.
func (s *ValkeyTenantOwnerStore) Owners(ctx context.Context, namespace string) (map[string]string, error) {
	return s.client.Do(ctx, s.client.B().Hgetall().Key(s.key(namespace)).Build()).AsStrMap()
}

// AddOwner implements TenantOwnerStore.
func (s *ValkeyTenantOwnerStore) AddOwner(ctx context.Context, namespace, uid, serverKey string) error {
	return s.client.Do(ctx, s.client.B().Hset().Key(s.key(namespace)).FieldValue().FieldValue(uid, serverKey).Build()).Error()
}

// RemoveOwners implements TenantOwnerStore.
func (s *ValkeyTenantOwnerStore) RemoveOwners(ctx context.Context, namespace string, uids ...string) error {
	if len(uids) == 0 {
		return nil
	}
	return s.client.Do(ctx, s.client.B().Hdel().Key(s.key(namespace)).Field(uids...).Build()).Error()
}

// DeleteNamespace implements TenantOwnerStore.
func (s *ValkeyTenantOwnerStore) DeleteNamespace(ctx context.Context, namespace string) error {
	return s.client.Do(ctx, s.client.B().Del().Key(s.key(namespace)).Build()).Error()
}
