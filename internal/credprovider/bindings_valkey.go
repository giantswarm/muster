package credprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/giantswarm/mcp-oauth/security"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
)

// ValkeyStore keeps bindings in Valkey, shared by every muster replica.
//
// Data model:
//
//	Key:   {keyPrefix}credprovider:binding:{sha256(actor)}
//	Value: [encrypted] JSON(Binding)
//	TTL:   the binding TTL, restarted by Put and Touch
//
// Put and Delete check the owner and write in one Lua script, so two replicas
// cannot race a bind past another person's binding.
//
// A binding that names an agent is also indexed:
//
//	Key:   {keyPrefix}credprovider:binding-agent:{sha256(agent)}
//	Value: sorted set of actors, scored by their binding's expiry (ms)
//	TTL:   the latest member's expiry; Put and Touch add or rescore the
//	       actor and remove members already expired, so the index lives
//	       exactly as long as the agent's bindings (bindings_valkey_index.go).
type ValkeyStore struct {
	client    valkey.Client
	keyPrefix string
	encryptor *security.Encryptor
	// now scores the agent index; time.Now outside tests.
	now func() time.Time
}

// NewValkeyStore returns a Store on the shared Valkey client. encryptor, when
// enabled, seals each value with AES-256-GCM like muster's token store.
func NewValkeyStore(client valkey.Client, keyPrefix string, encryptor *security.Encryptor) *ValkeyStore {
	return &ValkeyStore{client: client, keyPrefix: keyPrefix, encryptor: encryptor, now: time.Now}
}

func (s *ValkeyStore) key(actor string) string {
	sum := sha256.Sum256([]byte(actor))
	return s.keyPrefix + "credprovider:binding:" + hex.EncodeToString(sum[:])
}

func (s *ValkeyStore) seal(b Binding) (string, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	if s.encryptor == nil || !s.encryptor.IsEnabled() {
		return string(data), nil
	}
	return s.encryptor.Encrypt(string(data))
}

func (s *ValkeyStore) open(stored string) (*Binding, error) {
	data := stored
	if s.encryptor != nil && s.encryptor.IsEnabled() {
		plain, err := s.encryptor.Decrypt(stored)
		if err != nil {
			return nil, fmt.Errorf("decrypt binding: %w", err)
		}
		data = plain
	}
	var b Binding
	if err := json.Unmarshal([]byte(data), &b); err != nil {
		return nil, fmt.Errorf("decode binding: %w", err)
	}
	return &b, nil
}

// owner returns the subject of the actor's current binding, or "".
func (s *ValkeyStore) owner(ctx context.Context, actor string) (string, error) {
	b, err := s.Get(ctx, actor)
	if err != nil || b == nil {
		return "", err
	}
	return b.Subject, nil
}

// putScript writes ARGV[2] with TTL ARGV[3] ms unless the key holds a value
// other than ARGV[1], the value read when the owner was checked: a binding
// written by someone else in between makes the write fail (0).
var putScript = valkey.NewLuaScript(`
local cur = redis.call('GET', KEYS[1])
if cur and cur ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
return 1`)

// deleteScript deletes the key only while it still holds ARGV[1].
var deleteScript = valkey.NewLuaScript(`
local cur = redis.call('GET', KEYS[1])
if cur and cur ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[1])
return 1`)

// Put implements Store.
func (s *ValkeyStore) Put(ctx context.Context, actor string, b Binding, ttl time.Duration) error {
	key := s.key(actor)
	current, err := s.raw(ctx, key)
	if err != nil {
		return err
	}
	var existing *Binding
	if current != "" {
		if existing, err = s.open(current); err != nil {
			return err
		}
	}
	// The write below only lands while the key still holds current, so
	// this check holds for what it replaces.
	seal, sealed, err := s.readSeal(ctx, actor)
	if err != nil {
		return err
	}
	if err := checkReplace(existing, seal, sealed, b); err != nil {
		return err
	}
	value, err := s.seal(b)
	if err != nil {
		return fmt.Errorf("encode binding: %w", err)
	}
	ok, err := putScript.Exec(ctx, s.client, []string{key},
		[]string{current, value, fmt.Sprint(ttl.Milliseconds())}).AsInt64()
	if err != nil {
		return fmt.Errorf("write binding: %w", err)
	}
	if ok == 0 {
		// Changed between the read and the write: decide on the new owner.
		if owner, err := s.owner(ctx, actor); err == nil && owner != b.Subject {
			return ErrBoundToAnother
		}
		return fmt.Errorf("write binding: changed concurrently, retry")
	}
	if b.ApprovalSender {
		if err := s.writeSeal(ctx, actor, b.Sender, ttl); err != nil {
			_ = s.client.Do(ctx, s.client.B().Del().Key(key).Build()).Error()
			return err
		}
	}
	if b.Agent != "" {
		if err := s.index(ctx, b.Agent, actor, ttl); err != nil {
			// Without the index a dropped grant could not reach this
			// Session: refuse the bind rather than keep a mark nobody can
			// remove.
			_ = s.client.Do(ctx, s.client.B().Del().Key(key).Build()).Error()
			return fmt.Errorf("index binding by agent: %w", err)
		}
	}
	return nil
}

// replaceScript writes ARGV[2] keeping the key's TTL while it still holds
// ARGV[1].
var replaceScript = valkey.NewLuaScript(`
local cur = redis.call('GET', KEYS[1])
if cur ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'KEEPTTL')
return 1`)

// maxDropAttempts bounds the retries of a grant drop racing a rebind.
const maxDropAttempts = 5

// DropGrants implements Store.
func (s *ValkeyStore) DropGrants(ctx context.Context, actor, agent string, keep func(api.AgentApprovalGrant) bool) (int, error) {
	key := s.key(actor)
	for range maxDropAttempts {
		current, err := s.raw(ctx, key)
		if err != nil || current == "" {
			return 0, err
		}
		b, err := s.open(current)
		if err != nil {
			return 0, err
		}
		dropped := dropGrants(b, agent, keep)
		if dropped == 0 {
			return 0, nil
		}
		value, err := s.seal(*b)
		if err != nil {
			return 0, fmt.Errorf("encode binding: %w", err)
		}
		ok, err := replaceScript.Exec(ctx, s.client, []string{key}, []string{current, value}).AsInt64()
		if err != nil {
			return 0, fmt.Errorf("write binding: %w", err)
		}
		if ok == 1 {
			return dropped, nil
		}
	}
	return 0, fmt.Errorf("write binding: changed concurrently %d times, retry", maxDropAttempts)
}

// raw returns the stored value, "" when the key is missing.
func (s *ValkeyStore) raw(ctx context.Context, key string) (string, error) {
	v, err := s.client.Do(ctx, s.client.B().Get().Key(key).Build()).ToString()
	if valkey.IsValkeyNil(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read binding: %w", err)
	}
	return v, nil
}

// Get implements Store.
func (s *ValkeyStore) Get(ctx context.Context, actor string) (*Binding, error) {
	v, err := s.raw(ctx, s.key(actor))
	if err != nil || v == "" {
		return nil, err
	}
	return s.open(v)
}

// Touch implements Store.
func (s *ValkeyStore) Touch(ctx context.Context, actor, agent string, ttl time.Duration) error {
	err := s.client.Do(ctx, s.client.B().Pexpire().Key(s.key(actor)).Milliseconds(ttl.Milliseconds()).Build()).Error()
	if err != nil {
		return fmt.Errorf("touch binding: %w", err)
	}
	if agent != "" {
		// Only a sender writes an agent, so the binding has a seal to
		// keep ahead of it.
		if err := s.client.Do(ctx, s.client.B().Pexpire().Key(s.sealKey(actor)).Milliseconds((sealFactor * ttl).Milliseconds()).Build()).Error(); err != nil {
			return fmt.Errorf("touch binding: %w", err)
		}
		if err := s.index(ctx, agent, actor, ttl); err != nil {
			return fmt.Errorf("touch binding: %w", err)
		}
	}
	return nil
}

func (s *ValkeyStore) sealKey(actor string) string {
	sum := sha256.Sum256([]byte(actor))
	return s.keyPrefix + "credprovider:binding-sender:" + hex.EncodeToString(sum[:])
}

// readSeal returns the actor's sender seal (sealFactor.)
func (s *ValkeyStore) readSeal(ctx context.Context, actor string) (string, bool, error) {
	v, err := s.client.Do(ctx, s.client.B().Get().Key(s.sealKey(actor)).Build()).ToString()
	if valkey.IsValkeyNil(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read binding seal: %w", err)
	}
	return v, true, nil
}

// writeSeal records the sender of a sender-written binding beyond its
// expiry (sealFactor).
func (s *ValkeyStore) writeSeal(ctx context.Context, actor, sender string, ttl time.Duration) error {
	err := s.client.Do(ctx, s.client.B().Set().Key(s.sealKey(actor)).Value(sender).PxMilliseconds((sealFactor * ttl).Milliseconds()).Build()).Error()
	if err != nil {
		return fmt.Errorf("write binding seal: %w", err)
	}
	return nil
}

// Delete implements Store.
func (s *ValkeyStore) Delete(ctx context.Context, actor, subject, sender string) error {
	key := s.key(actor)
	current, err := s.raw(ctx, key)
	if err != nil || current == "" {
		return err
	}
	existing, err := s.open(current)
	if err != nil {
		return err
	}
	if err := checkDelete(existing, subject, sender); err != nil {
		return err
	}
	if err := deleteScript.Exec(ctx, s.client, []string{key}, []string{current}).Error(); err != nil {
		return fmt.Errorf("delete binding: %w", err)
	}
	return nil
}
