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
	// Namespaces returns the namespaces that hold a record, in no order:
	// those whose sign-ins a sweep at start must check against the
	// MCPServer objects that exist.
	Namespaces(ctx context.Context) ([]string, error)
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

// Namespaces implements TenantOwnerStore.
func (s *InMemoryTenantOwnerStore) Namespaces(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	namespaces := make([]string, 0, len(s.owners))
	for namespace := range s.owners {
		namespaces = append(namespaces, namespace)
	}
	return namespaces, nil
}

// ValkeyTenantOwnerStore keeps the records in Valkey, one hash per
// namespace with one field per server and one per issuer key, so adding a
// key is one HSET and never overwrites another writer's. No TTL: a record
// is small and lives as long as its server.
//
//	Key:    {keyPrefix}tenant_owners:{namespace}
//	Fields: {uid} -> {server key}
//	        {uid}/key/{issuer key} -> ""
//
// A set indexes the namespaces that hold a record, so Namespaces reads one
// key instead of scanning the keyspace:
//
//	Key:     {keyPrefix}tenant_owner_namespaces
//	Members: {namespace}
//
// The record and the set are in different hash slots, so they are written
// one after the other, never in one script or transaction. A write adds
// the namespace after its fields (AddOwner); a removal that leaves the record
// empty takes the namespace out and then puts it back if the record exists
// again (unindexIfEmpty). Every interleaving of two such writers that both
// complete leaves the namespace of a record in the set. A writer that fails
// or dies between its two commands can leave a record unindexed (the
// aggregator writes a server's record again on each of its registrations,
// which indexes it) or a member whose record is gone (an empty record has
// nothing to sweep).
//
// Records written before the index existed are indexed once, by one SCAN of
// the record keys, on the first Namespaces call that finds no
// {keyPrefix}tenant_owner_namespaces_indexed key; that call sets it.
type ValkeyTenantOwnerStore struct {
	client    valkey.Client
	keyPrefix string
}

// tenantOwnerScanCount is the COUNT of the SCAN that indexes the records
// written before the index existed.
const tenantOwnerScanCount = 100

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

func (s *ValkeyTenantOwnerStore) indexKey() string {
	return s.keyPrefix + "tenant_owner_namespaces"
}

func (s *ValkeyTenantOwnerStore) indexedKey() string {
	return s.keyPrefix + "tenant_owner_namespaces_indexed"
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
	if err := s.client.Do(ctx, cmd.Build()).Error(); err != nil {
		return err
	}
	return s.client.Do(ctx, s.client.B().Sadd().Key(s.indexKey()).Member(namespace).Build()).Error()
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
	if err := s.client.Do(ctx, s.client.B().Hdel().Key(s.key(namespace)).Field(remove...).Build()).Error(); err != nil {
		return err
	}
	return s.unindexIfEmpty(ctx, namespace)
}

// unindexIfEmpty takes a namespace whose record is gone out of the index.
// It removes the member before it checks the record again, so a writer that
// added fields meanwhile (and the member before or after) leaves it indexed.
func (s *ValkeyTenantOwnerStore) unindexIfEmpty(ctx context.Context, namespace string) error {
	exists := func() (bool, error) {
		n, err := s.client.Do(ctx, s.client.B().Exists().Key(s.key(namespace)).Build()).AsInt64()
		return n == 1, err
	}
	if ok, err := exists(); err != nil || ok {
		return err
	}
	if err := s.client.Do(ctx, s.client.B().Srem().Key(s.indexKey()).Member(namespace).Build()).Error(); err != nil {
		return err
	}
	if ok, err := exists(); err != nil || !ok {
		return err
	}
	return s.client.Do(ctx, s.client.B().Sadd().Key(s.indexKey()).Member(namespace).Build()).Error()
}

// Namespaces implements TenantOwnerStore.
func (s *ValkeyTenantOwnerStore) Namespaces(ctx context.Context) ([]string, error) {
	if err := s.indexEarlierRecords(ctx); err != nil {
		return nil, err
	}
	return s.client.Do(ctx, s.client.B().Smembers().Key(s.indexKey()).Build()).AsStrSlice()
}

// indexEarlierRecords indexes the records a muster without the index wrote,
// once: it scans the record keys unless the indexed key says a scan
// completed. A record removed during the scan leaves the index again.
func (s *ValkeyTenantOwnerStore) indexEarlierRecords(ctx context.Context) error {
	n, err := s.client.Do(ctx, s.client.B().Exists().Key(s.indexedKey()).Build()).AsInt64()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	prefix := s.key("")
	var cursor uint64
	for {
		entry, err := s.client.Do(ctx, s.client.B().Scan().Cursor(cursor).Match(prefix+"*").Count(tenantOwnerScanCount).Build()).AsScanEntry()
		if err != nil {
			return err
		}
		for _, key := range entry.Elements {
			namespace := strings.TrimPrefix(key, prefix)
			if err := s.client.Do(ctx, s.client.B().Sadd().Key(s.indexKey()).Member(namespace).Build()).Error(); err != nil {
				return err
			}
			if err := s.unindexIfEmpty(ctx, namespace); err != nil {
				return err
			}
		}
		cursor = entry.Cursor
		if cursor == 0 {
			break
		}
	}
	return s.client.Do(ctx, s.client.B().Set().Key(s.indexedKey()).Value("1").Build()).Error()
}
