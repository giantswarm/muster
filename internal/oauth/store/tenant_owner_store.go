package store

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/valkey-io/valkey-go"
)

// TenantOwner is one MCPServer object of an organization namespace whose
// sign-ins muster keeps: its server key and the issuer keys its sign-ins are
// filed under (api.IssuerKey).
type TenantOwner struct {
	Server     string   `json:"server"`
	IssuerKeys []string `json:"issuerKeys,omitempty"`
}

// TenantOwnerStore records, per organization namespace, the MCPServer
// objects (by Kubernetes UID) that own the namespace's stored OAuth state.
// A namespace's state belongs to the servers recorded here: the issuer keys
// of a recorded server that no longer exists are forgotten unless a server
// that is left uses them, and a namespace none of whose recorded servers
// exists any more is a new namespace of the same name (the aggregator's
// adoptTenantServer). The record lives wherever the tokens do (Valkey), so
// a namespace deleted and created again while muster was down is told
// apart as well.
type TenantOwnerStore interface {
	// Owners returns the recorded servers of the namespace by UID.
	Owners(ctx context.Context, namespace string) (map[string]TenantOwner, error)
	// AddOwner records a server of the namespace and adds issuer keys to
	// its record, atomically: two writers adding keys to the same server
	// lose none.
	AddOwner(ctx context.Context, namespace, uid string, owner TenantOwner) error
	// RemoveOwners forgets servers of the namespace; only these, so a
	// server another replica recorded meanwhile stays.
	RemoveOwners(ctx context.Context, namespace string, uids ...string) error
}

// InMemoryTenantOwnerStore is the TenantOwnerStore of a muster without
// Valkey; it dies with the process, as the tokens it guards do.
type InMemoryTenantOwnerStore struct {
	mu     sync.Mutex
	owners map[string]map[string]TenantOwner
}

// NewInMemoryTenantOwnerStore returns an empty in-memory store.
func NewInMemoryTenantOwnerStore() *InMemoryTenantOwnerStore {
	return &InMemoryTenantOwnerStore{owners: make(map[string]map[string]TenantOwner)}
}

// Owners implements TenantOwnerStore.
func (s *InMemoryTenantOwnerStore) Owners(_ context.Context, namespace string) (map[string]TenantOwner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	owners := make(map[string]TenantOwner, len(s.owners[namespace]))
	for uid, owner := range s.owners[namespace] {
		owner.IssuerKeys = slices.Clone(owner.IssuerKeys)
		owners[uid] = owner
	}
	return owners, nil
}

// AddOwner implements TenantOwnerStore.
func (s *InMemoryTenantOwnerStore) AddOwner(_ context.Context, namespace, uid string, owner TenantOwner) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners[namespace] == nil {
		s.owners[namespace] = make(map[string]TenantOwner)
	}
	recorded := s.owners[namespace][uid]
	recorded.Server = owner.Server
	for _, key := range owner.IssuerKeys {
		if !slices.Contains(recorded.IssuerKeys, key) {
			recorded.IssuerKeys = append(recorded.IssuerKeys, key)
		}
	}
	s.owners[namespace][uid] = recorded
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

// ValkeyTenantOwnerStore keeps the records in Valkey, one hash per
// namespace with one field per server and one per issuer key, so adding a
// key is one HSET and never overwrites another writer's. No TTL: a record
// is small and lives as long as its server.
//
//	Key:    {keyPrefix}tenant_owners:{namespace}
//	Fields: {uid} -> {server key}
//	        {uid}/key/{issuer key} -> ""
type ValkeyTenantOwnerStore struct {
	client    valkey.Client
	keyPrefix string
}

// tenantOwnerKeyField separates a UID from an issuer key in a field name. A
// Kubernetes UID holds no "/".
const tenantOwnerKeyField = "/key/"

// NewValkeyTenantOwnerStore returns a store on the shared Valkey client.
func NewValkeyTenantOwnerStore(client valkey.Client, keyPrefix string) *ValkeyTenantOwnerStore {
	return &ValkeyTenantOwnerStore{client: client, keyPrefix: keyPrefix}
}

func (s *ValkeyTenantOwnerStore) key(namespace string) string {
	return s.keyPrefix + "tenant_owners:" + namespace
}

// Owners implements TenantOwnerStore. An issuer key whose server field is
// missing still makes its UID an owner.
func (s *ValkeyTenantOwnerStore) Owners(ctx context.Context, namespace string) (map[string]TenantOwner, error) {
	fields, err := s.client.Do(ctx, s.client.B().Hgetall().Key(s.key(namespace)).Build()).AsStrMap()
	if err != nil {
		return nil, err
	}
	owners := make(map[string]TenantOwner)
	for field, value := range fields {
		uid, issuerKey, isKey := strings.Cut(field, tenantOwnerKeyField)
		owner := owners[uid]
		if isKey {
			owner.IssuerKeys = append(owner.IssuerKeys, issuerKey)
		} else {
			owner.Server = value
		}
		owners[uid] = owner
	}
	for uid, owner := range owners {
		slices.Sort(owner.IssuerKeys)
		owners[uid] = owner
	}
	return owners, nil
}

// AddOwner implements TenantOwnerStore.
func (s *ValkeyTenantOwnerStore) AddOwner(ctx context.Context, namespace, uid string, owner TenantOwner) error {
	cmd := s.client.B().Hset().Key(s.key(namespace)).FieldValue().FieldValue(uid, owner.Server)
	for _, issuerKey := range owner.IssuerKeys {
		cmd = cmd.FieldValue(uid+tenantOwnerKeyField+issuerKey, "")
	}
	return s.client.Do(ctx, cmd.Build()).Error()
}

// RemoveOwners implements TenantOwnerStore.
func (s *ValkeyTenantOwnerStore) RemoveOwners(ctx context.Context, namespace string, uids ...string) error {
	if len(uids) == 0 {
		return nil
	}
	fields, err := s.client.Do(ctx, s.client.B().Hkeys().Key(s.key(namespace)).Build()).AsStrSlice()
	if err != nil {
		return err
	}
	var remove []string
	for _, field := range fields {
		uid, _, _ := strings.Cut(field, tenantOwnerKeyField)
		if slices.Contains(uids, uid) {
			remove = append(remove, field)
		}
	}
	if len(remove) == 0 {
		return nil
	}
	return s.client.Do(ctx, s.client.B().Hdel().Key(s.key(namespace)).Field(remove...).Build()).Error()
}
