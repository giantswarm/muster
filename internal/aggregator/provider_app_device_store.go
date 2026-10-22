package aggregator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/giantswarm/mcp-oauth/security"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/chatgptlogin"
)

const (
	// deviceCodeGrace keeps an outstanding code past its expiry, long
	// enough for its poll to report the expiry.
	deviceCodeGrace = time.Minute

	// deviceFailureTTL is how long a sign-in's last failure is reported.
	deviceFailureTTL = 24 * time.Hour
)

// deviceSignInStore keeps the outstanding device code of each person and
// App, the lease of the replica polling it and the last failure. A store
// shared by the replicas makes them answer one code and poll it once.
type deviceSignInStore interface {
	// claim records code as the outstanding one unless one is; it returns
	// the outstanding code, code itself when claimed.
	claim(ctx context.Context, k deviceSignInKey, code *chatgptlogin.Code) (*chatgptlogin.Code, error)
	// outstanding returns the outstanding code, nil when none is.
	outstanding(ctx context.Context, k deviceSignInKey) (*chatgptlogin.Code, error)
	// release removes the outstanding code while it is userCode, any code
	// when userCode is "", and reports whether it removed one.
	release(ctx context.Context, k deviceSignInKey, userCode string) (bool, error)
	// lease takes or renews the poll lease for holder; false while another
	// holder has it.
	lease(ctx context.Context, k deviceSignInKey, holder string, ttl time.Duration) (bool, error)
	// unlease gives the lease up while holder has it.
	unlease(ctx context.Context, k deviceSignInKey, holder string) error
	// setFailure records the last failure; "" forgets it.
	setFailure(ctx context.Context, k deviceSignInKey, reason string) error
	// failure returns the last failure, "" when none is recorded.
	failure(ctx context.Context, k deviceSignInKey) (string, error)
}

// codeDeadline is when an outstanding code is forgotten.
func codeDeadline(code *chatgptlogin.Code) time.Time {
	return code.ExpiresAt.Add(deviceCodeGrace)
}

// memoryDeviceSignInStore is the store of a single replica.
type memoryDeviceSignInStore struct {
	mu       sync.Mutex
	codes    map[deviceSignInKey]*chatgptlogin.Code
	leases   map[deviceSignInKey]memoryLease
	failures map[deviceSignInKey]string
}

type memoryLease struct {
	holder string
	until  time.Time
}

func newMemoryDeviceSignInStore() *memoryDeviceSignInStore {
	return &memoryDeviceSignInStore{
		codes:    make(map[deviceSignInKey]*chatgptlogin.Code),
		leases:   make(map[deviceSignInKey]memoryLease),
		failures: make(map[deviceSignInKey]string),
	}
}

// current returns the outstanding code; the caller holds mu.
func (s *memoryDeviceSignInStore) current(k deviceSignInKey) *chatgptlogin.Code {
	code := s.codes[k]
	if code != nil && time.Now().After(codeDeadline(code)) {
		delete(s.codes, k)
		return nil
	}
	return code
}

func (s *memoryDeviceSignInStore) claim(_ context.Context, k deviceSignInKey, code *chatgptlogin.Code) (*chatgptlogin.Code, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.current(k); current != nil {
		return current, nil
	}
	s.codes[k] = code
	return code, nil
}

func (s *memoryDeviceSignInStore) outstanding(_ context.Context, k deviceSignInKey) (*chatgptlogin.Code, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current(k), nil
}

func (s *memoryDeviceSignInStore) release(_ context.Context, k deviceSignInKey, userCode string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.current(k)
	if current == nil || (userCode != "" && current.UserCode != userCode) {
		return false, nil
	}
	delete(s.codes, k)
	return true, nil
}

func (s *memoryDeviceSignInStore) lease(_ context.Context, k deviceSignInKey, holder string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if l, ok := s.leases[k]; ok && l.holder != holder && now.Before(l.until) {
		return false, nil
	}
	s.leases[k] = memoryLease{holder: holder, until: now.Add(ttl)}
	return true, nil
}

func (s *memoryDeviceSignInStore) unlease(_ context.Context, k deviceSignInKey, holder string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[k].holder == holder {
		delete(s.leases, k)
	}
	return nil
}

func (s *memoryDeviceSignInStore) setFailure(_ context.Context, k deviceSignInKey, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reason == "" {
		delete(s.failures, k)
	} else {
		s.failures[k] = reason
	}
	return nil
}

func (s *memoryDeviceSignInStore) failure(_ context.Context, k deviceSignInKey) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failures[k], nil
}

// valkeyDeviceSignInStore is the store the replicas share.
//
// Data model, {h} = sha256(person, App):
//
//	{keyPrefix}provider-app:device:code:{h}     [encrypted] JSON(chatgptlogin.Code), TTL to the code's expiry plus deviceCodeGrace
//	{keyPrefix}provider-app:device:poll:{h}     the lease holder, TTL the lease
//	{keyPrefix}provider-app:device:failure:{h}  the last failure, TTL deviceFailureTTL
type valkeyDeviceSignInStore struct {
	client    valkey.Client
	keyPrefix string
	encryptor *security.Encryptor
}

func newValkeyDeviceSignInStore(client valkey.Client, keyPrefix string, encryptor *security.Encryptor) *valkeyDeviceSignInStore {
	return &valkeyDeviceSignInStore{client: client, keyPrefix: keyPrefix, encryptor: encryptor}
}

func (s *valkeyDeviceSignInStore) key(kind string, k deviceSignInKey) string {
	sum := sha256.Sum256([]byte(k.sub + "\x00" + k.app))
	return s.keyPrefix + "provider-app:device:" + kind + ":" + hex.EncodeToString(sum[:])
}

func (s *valkeyDeviceSignInStore) seal(code *chatgptlogin.Code) (string, error) {
	data, err := json.Marshal(code)
	if err != nil {
		return "", err
	}
	if s.encryptor == nil || !s.encryptor.IsEnabled() {
		return string(data), nil
	}
	return s.encryptor.Encrypt(string(data))
}

func (s *valkeyDeviceSignInStore) open(stored string) (*chatgptlogin.Code, error) {
	data := stored
	if s.encryptor != nil && s.encryptor.IsEnabled() {
		plain, err := s.encryptor.Decrypt(stored)
		if err != nil {
			return nil, fmt.Errorf("decrypt device code: %w", err)
		}
		data = plain
	}
	var code chatgptlogin.Code
	if err := json.Unmarshal([]byte(data), &code); err != nil {
		return nil, fmt.Errorf("decode device code: %w", err)
	}
	return &code, nil
}

// get returns a key's value, "" when it is absent.
func (s *valkeyDeviceSignInStore) get(ctx context.Context, key string) (string, error) {
	value, err := s.client.Do(ctx, s.client.B().Get().Key(key).Build()).ToString()
	if valkey.IsValkeyNil(err) {
		return "", nil
	}
	return value, err
}

func (s *valkeyDeviceSignInStore) claim(ctx context.Context, k deviceSignInKey, code *chatgptlogin.Code) (*chatgptlogin.Code, error) {
	value, err := s.seal(code)
	if err != nil {
		return nil, fmt.Errorf("encode device code: %w", err)
	}
	ttl := max(time.Until(codeDeadline(code)), time.Second)
	key := s.key("code", k)
	err = s.client.Do(ctx, s.client.B().Set().Key(key).Value(value).Nx().PxMilliseconds(ttl.Milliseconds()).Build()).Error()
	if err == nil {
		return code, nil
	}
	if !valkey.IsValkeyNil(err) {
		return nil, fmt.Errorf("record device code: %w", err)
	}
	// NX refused: another replica's code is the person's.
	current, err := s.outstanding(ctx, k)
	if err != nil || current != nil {
		return current, err
	}
	return nil, fmt.Errorf("record device code: the outstanding code expired while it was read")
}

func (s *valkeyDeviceSignInStore) outstanding(ctx context.Context, k deviceSignInKey) (*chatgptlogin.Code, error) {
	stored, err := s.get(ctx, s.key("code", k))
	if err != nil || stored == "" {
		return nil, err
	}
	return s.open(stored)
}

// deleteIfScript deletes KEYS[1] while it holds ARGV[1].
var deleteIfScript = valkey.NewLuaScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`)

// leaseScript sets KEYS[1] to ARGV[1] for ARGV[2] ms unless another holder
// has it.
var leaseScript = valkey.NewLuaScript(`
local cur = redis.call('GET', KEYS[1])
if cur and cur ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return 1`)

func (s *valkeyDeviceSignInStore) release(ctx context.Context, k deviceSignInKey, userCode string) (bool, error) {
	key := s.key("code", k)
	if userCode == "" {
		n, err := s.client.Do(ctx, s.client.B().Del().Key(key).Build()).AsInt64()
		return n > 0, err
	}
	stored, err := s.get(ctx, key)
	if err != nil || stored == "" {
		return false, err
	}
	code, err := s.open(stored)
	if err != nil {
		return false, err
	}
	if code.UserCode != userCode {
		return false, nil
	}
	n, err := deleteIfScript.Exec(ctx, s.client, []string{key}, []string{stored}).AsInt64()
	return n > 0, err
}

func (s *valkeyDeviceSignInStore) lease(ctx context.Context, k deviceSignInKey, holder string, ttl time.Duration) (bool, error) {
	n, err := leaseScript.Exec(ctx, s.client, []string{s.key("poll", k)}, []string{holder, fmt.Sprint(ttl.Milliseconds())}).AsInt64()
	return n == 1, err
}

func (s *valkeyDeviceSignInStore) unlease(ctx context.Context, k deviceSignInKey, holder string) error {
	return deleteIfScript.Exec(ctx, s.client, []string{s.key("poll", k)}, []string{holder}).Error()
}

func (s *valkeyDeviceSignInStore) setFailure(ctx context.Context, k deviceSignInKey, reason string) error {
	key := s.key("failure", k)
	if reason == "" {
		return s.client.Do(ctx, s.client.B().Del().Key(key).Build()).Error()
	}
	return s.client.Do(ctx, s.client.B().Set().Key(key).Value(reason).PxMilliseconds(deviceFailureTTL.Milliseconds()).Build()).Error()
}

func (s *valkeyDeviceSignInStore) failure(ctx context.Context, k deviceSignInKey) (string, error) {
	return s.get(ctx, s.key("failure", k))
}
