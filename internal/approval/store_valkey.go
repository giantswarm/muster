package approval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/giantswarm/mcp-oauth/security"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
)

// The caps on pending approvals: an agent looping on a call that asks cannot
// fill the store or the person's screen.
const (
	// MaxPendingPerPerson caps the pending approvals of one person.
	MaxPendingPerPerson = 10
	// MaxPendingPerSession caps the pending approvals of one person in one
	// muster session.
	MaxPendingPerSession = 3
)

// decision is what the store keeps of a decision, apart from the request.
// It names the request and its binding, and is sealed like the request when
// the store's encryptor is enabled: a decision written by anyone but muster
// (without the key) or moved to another request is no approval.
type decision struct {
	Approved bool               `json:"approved"`
	By       api.SignInIdentity `json:"by"`
	ID       string             `json:"id,omitempty"`
	Binding  string             `json:"binding,omitempty"`
}

// ValkeyStore keeps approvals in Valkey, so every replica sees a request and
// its decision, and a retry consumes an approval exactly once wherever it
// lands (GETDEL of the decision). Requests and flows are encrypted at rest
// when the store's encryptor is enabled; every key expires with its request.
type ValkeyStore struct {
	client    valkey.Client
	prefix    string
	encryptor *security.Encryptor
	now       func() time.Time
}

var _ api.ApprovalStore = (*ValkeyStore)(nil)

// NewValkeyStore returns a store on client under keyPrefix.
func NewValkeyStore(client valkey.Client, keyPrefix string, encryptor *security.Encryptor) *ValkeyStore {
	// The hash tag keeps every approval key in one cluster slot, so the
	// scripts below can touch a request's keys together.
	return &ValkeyStore{client: client, prefix: keyPrefix + "approval:{a}:", encryptor: encryptor, now: clock.Now}
}

func (s *ValkeyStore) requestKey(id string) string  { return s.prefix + "req:" + id }
func (s *ValkeyStore) bindingKey(key string) string { return s.prefix + "bind:" + key }
func (s *ValkeyStore) decisionKey(id string) string { return s.prefix + "decision:" + id }
func (s *ValkeyStore) flowKey(key string) string    { return s.prefix + "flow:" + key }
func (s *ValkeyStore) handoffKey(key string) string { return s.prefix + "handoff:" + key }
func (s *ValkeyStore) personKey(i api.SignInIdentity) string {
	return s.prefix + "person:" + digest(i.Issuer+"\x00"+i.Subject)
}
func (s *ValkeyStore) sessionKey(i api.SignInIdentity, sessionID string) string {
	return s.prefix + "session:" + digest(i.Issuer+"\x00"+i.Subject+"\x00"+sessionID)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *ValkeyStore) seal(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if s.encryptor == nil {
		return string(raw), nil
	}
	return s.encryptor.Encrypt(string(raw))
}

func (s *ValkeyStore) open(sealed string, into any) error {
	plain := sealed
	if s.encryptor != nil {
		var err error
		if plain, err = s.encryptor.Decrypt(sealed); err != nil {
			return err
		}
	}
	return json.Unmarshal([]byte(plain), into)
}

// getString returns a key's value, ok false when the key does not exist.
func (s *ValkeyStore) getString(ctx context.Context, key string) (string, bool, error) {
	value, err := s.client.Do(ctx, s.client.B().Get().Key(key).Build()).ToString()
	if valkey.IsValkeyNil(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("approval store: %w", err)
	}
	return value, true, nil
}

// load reads a request and its decision.
func (s *ValkeyStore) load(ctx context.Context, id string) (*api.ApprovalRequest, error) {
	sealed, ok, err := s.getString(ctx, s.requestKey(id))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, api.ErrApprovalNotFound
	}
	var request api.ApprovalRequest
	if err := s.open(sealed, &request); err != nil {
		return nil, fmt.Errorf("approval store: read request: %w", err)
	}
	if !s.now().Before(request.ExpiresAt) {
		return nil, api.ErrApprovalNotFound
	}
	request.Status = api.ApprovalStatusPending
	raw, ok, err := s.getString(ctx, s.decisionKey(id))
	if err != nil {
		return nil, err
	}
	if ok {
		s.applyDecision(&request, raw)
	}
	return &request, nil
}

// applyDecision sets the request's status from a stored decision. A
// decision muster cannot open, or one written for another request or
// binding, is no approval: the request reads as declined.
func (s *ValkeyStore) applyDecision(request *api.ApprovalRequest, raw string) {
	request.Status = api.ApprovalStatusDeclined
	var d decision
	if s.open(raw, &d) != nil || d.ID != request.ID || d.Binding != request.BindingKey() {
		return
	}
	if d.Approved {
		request.Status = api.ApprovalStatusApproved
	}
	request.DecidedBy = d.By
}

func (s *ValkeyStore) ttl(request *api.ApprovalRequest) (int64, error) {
	ms := request.ExpiresAt.Sub(s.now()).Milliseconds()
	if ms <= 0 {
		return 0, api.ErrApprovalNotFound
	}
	return ms, nil
}

// Create implements api.ApprovalStore.
func (s *ValkeyStore) Create(ctx context.Context, request *api.ApprovalRequest) (*api.ApprovalRequest, error) {
	binding := request.BindingKey()
	if existing, err := s.Find(ctx, binding); err == nil {
		return existing, nil
	} else if !errors.Is(err, api.ErrApprovalNotFound) {
		return nil, err
	}

	ttl, err := s.ttl(request)
	if err != nil {
		return nil, err
	}
	sealed, err := s.seal(request)
	if err != nil {
		return nil, fmt.Errorf("approval store: write request: %w", err)
	}
	// The request first, then the binding with NX: a concurrent identical
	// call that won the binding is returned instead of this one.
	if err := s.client.Do(ctx, s.client.B().Set().Key(s.requestKey(request.ID)).Value(sealed).PxMilliseconds(ttl).Build()).Error(); err != nil {
		return nil, fmt.Errorf("approval store: %w", err)
	}
	if err := s.claimBinding(ctx, binding, request.ID, ttl); err != nil {
		_ = s.client.Do(ctx, s.client.B().Del().Key(s.requestKey(request.ID)).Build()).Error()
		if errors.Is(err, errBindingTaken) {
			return s.Find(ctx, binding)
		}
		return nil, err
	}
	// Then a slot under both caps, checked and taken in one script; without
	// one the request and its binding go again.
	if err := s.reserve(ctx, request, ttl); err != nil {
		_ = s.client.DoMulti(ctx,
			s.client.B().Del().Key(s.requestKey(request.ID)).Build(),
			s.client.B().Del().Key(s.bindingKey(binding)).Build())
		return nil, err
	}
	created := *request
	created.Status = api.ApprovalStatusPending
	return &created, nil
}

// Find implements api.ApprovalStore.
func (s *ValkeyStore) Find(ctx context.Context, bindingKey string) (*api.ApprovalRequest, error) {
	id, ok, err := s.getString(ctx, s.bindingKey(bindingKey))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, api.ErrApprovalNotFound
	}
	request, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if request.BindingKey() != bindingKey {
		return nil, api.ErrApprovalNotFound
	}
	return request, nil
}

// consumeScript takes a request's decision and, with it, removes the
// request and its binding in one step: of concurrent retries one gets the
// decision, and every other one finds the request gone (so it asks anew
// instead of answering with a link to a used request). Without a decision it
// reports whether the request still exists.
// KEYS: decision, request, binding. ARGV: request id.
var consumeScript = valkey.NewLuaScript(`
local d = redis.call('GETDEL', KEYS[1])
if d then
  redis.call('DEL', KEYS[2])
  if redis.call('GET', KEYS[3]) == ARGV[1] then redis.call('DEL', KEYS[3]) end
  return {1, d}
end
return {0, tostring(redis.call('EXISTS', KEYS[2]))}`)

// Consume implements api.ApprovalStore.
func (s *ValkeyStore) Consume(ctx context.Context, bindingKey string) (*api.ApprovalRequest, error) {
	request, err := s.Find(ctx, bindingKey)
	if err != nil {
		return nil, err
	}
	reply, err := consumeScript.Exec(ctx, s.client,
		[]string{s.decisionKey(request.ID), s.requestKey(request.ID), s.bindingKey(bindingKey)},
		[]string{request.ID}).ToArray()
	if err != nil {
		return nil, fmt.Errorf("approval store: %w", err)
	}
	if len(reply) != 2 {
		return nil, fmt.Errorf("approval store: unexpected reply to consume")
	}
	taken, _ := reply[0].AsInt64()
	value, _ := reply[1].ToString()
	if taken == 0 {
		if value == "0" {
			// A concurrent retry took the decision and removed the request.
			return nil, api.ErrApprovalNotFound
		}
		request.Status = api.ApprovalStatusPending
		return request, nil
	}
	s.applyDecision(request, value)
	return request, nil
}

// Get implements api.ApprovalStore.
func (s *ValkeyStore) Get(ctx context.Context, id string) (*api.ApprovalRequest, error) {
	return s.load(ctx, id)
}

// Decide implements api.ApprovalStore.
func (s *ValkeyStore) Decide(ctx context.Context, id string, approve bool, by api.SignInIdentity) error {
	request, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	if request.Status != api.ApprovalStatusPending {
		return api.ErrApprovalDecided
	}
	ttl, err := s.ttl(request)
	if err != nil {
		return err
	}
	sealed, err := s.seal(decision{Approved: approve, By: by, ID: id, Binding: request.BindingKey()})
	if err != nil {
		return err
	}
	set, err := s.client.Do(ctx, s.client.B().Set().Key(s.decisionKey(id)).Value(sealed).Nx().PxMilliseconds(ttl).Build()).ToString()
	if valkey.IsValkeyNil(err) || (err == nil && set != "OK") {
		return api.ErrApprovalDecided
	}
	if err != nil {
		return fmt.Errorf("approval store: %w", err)
	}
	// A decided request no longer waits for the person: it stops counting
	// against the caps.
	_ = s.client.DoMulti(ctx,
		s.client.B().Zrem().Key(s.personKey(request.Identity)).Member(id).Build(),
		s.client.B().Zrem().Key(s.sessionKey(request.Identity, request.SessionID)).Member(id).Build())
	return nil
}

// Ping implements api.ApprovalStore.
func (s *ValkeyStore) Ping(ctx context.Context) error {
	if err := s.client.Do(ctx, s.client.B().Ping().Build()).Error(); err != nil {
		return fmt.Errorf("approval store: %w", err)
	}
	return nil
}

// PutFlow implements api.ApprovalStore.
func (s *ValkeyStore) PutFlow(ctx context.Context, key string, flow api.ApprovalPageFlow, ttl time.Duration) error {
	sealed, err := s.seal(flow)
	if err != nil {
		return err
	}
	if err := s.client.Do(ctx, s.client.B().Set().Key(s.flowKey(key)).Value(sealed).PxMilliseconds(ttl.Milliseconds()).Build()).Error(); err != nil {
		return fmt.Errorf("approval store: %w", err)
	}
	return nil
}

// GetFlow implements api.ApprovalStore.
func (s *ValkeyStore) GetFlow(ctx context.Context, key string) (*api.ApprovalPageFlow, error) {
	sealed, ok, err := s.getString(ctx, s.flowKey(key))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, api.ErrApprovalNotFound
	}
	var flow api.ApprovalPageFlow
	if err := s.open(sealed, &flow); err != nil {
		return nil, fmt.Errorf("approval store: read flow: %w", err)
	}
	return &flow, nil
}

// DeleteFlow implements api.ApprovalStore.
func (s *ValkeyStore) DeleteFlow(ctx context.Context, key string) error {
	if err := s.client.Do(ctx, s.client.B().Del().Key(s.flowKey(key)).Build()).Error(); err != nil {
		return fmt.Errorf("approval store: %w", err)
	}
	return nil
}

// PutHandoff implements api.ApprovalStore.
func (s *ValkeyStore) PutHandoff(ctx context.Context, key string, handoff api.SignInHandoff, ttl time.Duration) error {
	sealed, err := s.seal(handoff)
	if err != nil {
		return err
	}
	if err := s.client.Do(ctx, s.client.B().Set().Key(s.handoffKey(key)).Value(sealed).PxMilliseconds(ttl.Milliseconds()).Build()).Error(); err != nil {
		return fmt.Errorf("approval store: %w", err)
	}
	return nil
}

// TakeHandoff implements api.ApprovalStore: GETDEL, so of concurrent
// redemptions on any replica exactly one gets the handoff.
func (s *ValkeyStore) TakeHandoff(ctx context.Context, key string) (*api.SignInHandoff, error) {
	sealed, err := s.client.Do(ctx, s.client.B().Getdel().Key(s.handoffKey(key)).Build()).ToString()
	if valkey.IsValkeyNil(err) {
		return nil, api.ErrApprovalNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("approval store: %w", err)
	}
	var handoff api.SignInHandoff
	if err := s.open(sealed, &handoff); err != nil {
		return nil, fmt.Errorf("approval store: read handoff: %w", err)
	}
	return &handoff, nil
}

// reserveScript drops expired members from the person's and the Session's
// sets of pending requests and, when both are under their caps, adds the
// request to both, in one step. It returns 0 when added, 1 when
// the person's cap is reached, 2 when the Session's.
// KEYS: person, session. ARGV: now ms, expiry ms, id, ttl ms, person cap,
// session cap.
var reserveScript = valkey.NewLuaScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', ARGV[1])
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[5]) then return 1 end
if redis.call('ZCARD', KEYS[2]) >= tonumber(ARGV[6]) then return 2 end
for i = 1, 2 do
  redis.call('ZADD', KEYS[i], ARGV[2], ARGV[3])
  redis.call('PEXPIRE', KEYS[i], ARGV[4])
end
return 0`)

// reserve takes a slot for request under the caps.
func (s *ValkeyStore) reserve(ctx context.Context, request *api.ApprovalRequest, ttl int64) error {
	code, err := reserveScript.Exec(ctx, s.client,
		[]string{s.personKey(request.Identity), s.sessionKey(request.Identity, request.SessionID)},
		[]string{
			strconv.FormatInt(s.now().UnixMilli(), 10),
			strconv.FormatInt(request.ExpiresAt.UnixMilli(), 10),
			request.ID,
			strconv.FormatInt(ttl, 10),
			strconv.Itoa(MaxPendingPerPerson),
			strconv.Itoa(MaxPendingPerSession),
		}).AsInt64()
	switch {
	case err != nil:
		return fmt.Errorf("approval store: %w", err)
	case code == 1:
		return &api.ApprovalLimitError{Scope: api.ApprovalLimitPerson, Limit: MaxPendingPerPerson}
	case code == 2:
		return &api.ApprovalLimitError{Scope: api.ApprovalLimitSession, Limit: MaxPendingPerSession}
	}
	return nil
}

// errBindingTaken is a binding a live request holds.
var errBindingTaken = errors.New("approval binding taken")

// claimBinding points the binding at id unless a live request holds it. A
// binding left by a request that expired by muster's clock (its keys not yet
// expired in Valkey) is taken over.
func (s *ValkeyStore) claimBinding(ctx context.Context, binding, id string, ttl int64) error {
	for range 2 {
		set, err := s.client.Do(ctx, s.client.B().Set().Key(s.bindingKey(binding)).Value(id).Nx().PxMilliseconds(ttl).Build()).ToString()
		if err == nil && set == "OK" {
			return nil
		}
		if err != nil && !valkey.IsValkeyNil(err) {
			return fmt.Errorf("approval store: %w", err)
		}
		if _, err := s.Find(ctx, binding); !errors.Is(err, api.ErrApprovalNotFound) {
			return errBindingTaken
		}
		if err := s.client.Do(ctx, s.client.B().Del().Key(s.bindingKey(binding)).Build()).Error(); err != nil {
			return fmt.Errorf("approval store: %w", err)
		}
	}
	return errBindingTaken
}
