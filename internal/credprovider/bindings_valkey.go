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
)

// ValkeyStore keeps bindings in Valkey, shared by every muster replica.
//
// Data model:
//
//	Key:   {keyPrefix}credprovider:binding:{sha256(actor)}
//	Value: [encrypted] JSON(Binding)
//	TTL:   the binding TTL, restarted by Put and Touch
//
// Put, PinActorUID and Delete write in one Lua script that checks the value is
// still the one read, so two replicas cannot race a bind past another
// person's binding or a pin past another uid.
type ValkeyStore struct {
	client    valkey.Client
	keyPrefix string
	encryptor *security.Encryptor
}

// NewValkeyStore returns a Store on the shared Valkey client. encryptor, when
// enabled, seals each value with AES-256-GCM like muster's token store.
func NewValkeyStore(client valkey.Client, keyPrefix string, encryptor *security.Encryptor) *ValkeyStore {
	return &ValkeyStore{client: client, keyPrefix: keyPrefix, encryptor: encryptor}
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

// pinScript replaces the value with ARGV[2], keeping its TTL, only while it
// still holds ARGV[1].
var pinScript = valkey.NewLuaScript(`
local cur = redis.call('GET', KEYS[1])
if cur ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'KEEPTTL')
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
	if current != "" {
		existing, err := s.open(current)
		if err != nil {
			return err
		}
		if existing.Subject != b.Subject {
			return ErrBoundToAnother
		}
		if b.ActorUID == "" {
			b.ActorUID = existing.ActorUID
		}
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
	return nil
}

// PinActorUID implements Store.
func (s *ValkeyStore) PinActorUID(ctx context.Context, actor, uid string) error {
	key := s.key(actor)
	current, err := s.raw(ctx, key)
	if err != nil || current == "" {
		return err
	}
	b, err := s.open(current)
	if err != nil {
		return err
	}
	changed, err := pinActorUID(b, uid)
	if err != nil || !changed {
		return err
	}
	value, err := s.seal(*b)
	if err != nil {
		return fmt.Errorf("encode binding: %w", err)
	}
	ok, err := pinScript.Exec(ctx, s.client, []string{key}, []string{current, value}).AsInt64()
	if err != nil {
		return fmt.Errorf("pin binding: %w", err)
	}
	if ok == 0 {
		// Changed between the read and the write: decide on the new value.
		now, err := s.Get(ctx, actor)
		if err != nil {
			return err
		}
		if now == nil || now.ActorUID == uid {
			return nil
		}
		if now.ActorUID != "" {
			return ErrActorUIDMismatch
		}
		return fmt.Errorf("pin binding: changed concurrently, retry")
	}
	return nil
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
func (s *ValkeyStore) Touch(ctx context.Context, actor string, ttl time.Duration) error {
	err := s.client.Do(ctx, s.client.B().Pexpire().Key(s.key(actor)).Milliseconds(ttl.Milliseconds()).Build()).Error()
	if err != nil {
		return fmt.Errorf("touch binding: %w", err)
	}
	return nil
}

// Delete implements Store.
func (s *ValkeyStore) Delete(ctx context.Context, actor, subject string) error {
	key := s.key(actor)
	current, err := s.raw(ctx, key)
	if err != nil || current == "" {
		return err
	}
	existing, err := s.open(current)
	if err != nil {
		return err
	}
	if existing.Subject != subject {
		return ErrBoundToAnother
	}
	if err := deleteScript.Exec(ctx, s.client, []string{key}, []string{current}).Error(); err != nil {
		return fmt.Errorf("delete binding: %w", err)
	}
	return nil
}
