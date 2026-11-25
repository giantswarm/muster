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
type decision struct {
	Approved bool               `json:"approved"`
	By       api.SignInIdentity `json:"by"`
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
	return &ValkeyStore{client: client, prefix: keyPrefix + "approval:", encryptor: encryptor, now: time.Now}
}

func (s *ValkeyStore) requestKey(id string) string  { return s.prefix + "req:" + id }
func (s *ValkeyStore) bindingKey(key string) string { return s.prefix + "bind:" + key }
func (s *ValkeyStore) decisionKey(id string) string { return s.prefix + "decision:" + id }
func (s *ValkeyStore) flowKey(key string) string    { return s.prefix + "flow:" + key }
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
		applyDecision(&request, raw)
	}
	return &request, nil
}

func applyDecision(request *api.ApprovalRequest, raw string) {
	var d decision
	if json.Unmarshal([]byte(raw), &d) != nil {
		// An unreadable decision is no approval.
		request.Status = api.ApprovalStatusDeclined
		return
	}
	request.Status = api.ApprovalStatusDeclined
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
	now := strconv.FormatInt(s.now().UnixMilli(), 10)
	for key, limit := range map[string]int64{s.personKey(request.Identity): MaxPendingPerPerson, s.sessionKey(request.Identity, request.SessionID): MaxPendingPerSession} {
		if err := s.client.Do(ctx, s.client.B().Zremrangebyscore().Key(key).Min("-inf").Max(now).Build()).Error(); err != nil {
			return nil, fmt.Errorf("approval store: %w", err)
		}
		count, err := s.client.Do(ctx, s.client.B().Zcard().Key(key).Build()).AsInt64()
		if err != nil {
			return nil, fmt.Errorf("approval store: %w", err)
		}
		if count >= limit {
			return nil, api.ErrApprovalLimit
		}
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
	set, err := s.client.Do(ctx, s.client.B().Set().Key(s.bindingKey(binding)).Value(request.ID).Nx().PxMilliseconds(ttl).Build()).ToString()
	if valkey.IsValkeyNil(err) || (err == nil && set != "OK") {
		_ = s.client.Do(ctx, s.client.B().Del().Key(s.requestKey(request.ID)).Build()).Error()
		return s.Find(ctx, binding)
	}
	if err != nil {
		return nil, fmt.Errorf("approval store: %w", err)
	}
	expires := float64(request.ExpiresAt.UnixMilli())
	for _, key := range []string{s.personKey(request.Identity), s.sessionKey(request.Identity, request.SessionID)} {
		cmds := valkey.Commands{
			s.client.B().Zadd().Key(key).ScoreMember().ScoreMember(expires, request.ID).Build(),
			s.client.B().Pexpire().Key(key).Milliseconds(ttl).Build(),
		}
		for _, result := range s.client.DoMulti(ctx, cmds...) {
			if err := result.Error(); err != nil {
				return nil, fmt.Errorf("approval store: %w", err)
			}
		}
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

// Consume implements api.ApprovalStore.
func (s *ValkeyStore) Consume(ctx context.Context, bindingKey string) (*api.ApprovalRequest, error) {
	request, err := s.Find(ctx, bindingKey)
	if err != nil {
		return nil, err
	}
	raw, err := s.client.Do(ctx, s.client.B().Getdel().Key(s.decisionKey(request.ID)).Build()).ToString()
	if valkey.IsValkeyNil(err) {
		request.Status = api.ApprovalStatusPending
		return request, nil
	}
	if err != nil {
		return nil, fmt.Errorf("approval store: %w", err)
	}
	applyDecision(request, raw)
	s.remove(ctx, request)
	return request, nil
}

// remove deletes a consumed request; a key left behind expires with it.
func (s *ValkeyStore) remove(ctx context.Context, request *api.ApprovalRequest) {
	cmds := valkey.Commands{
		s.client.B().Del().Key(s.requestKey(request.ID)).Build(),
		s.client.B().Del().Key(s.bindingKey(request.BindingKey())).Build(),
		s.client.B().Zrem().Key(s.personKey(request.Identity)).Member(request.ID).Build(),
		s.client.B().Zrem().Key(s.sessionKey(request.Identity, request.SessionID)).Member(request.ID).Build(),
	}
	_ = s.client.DoMulti(ctx, cmds...)
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
	raw, err := json.Marshal(decision{Approved: approve, By: by})
	if err != nil {
		return err
	}
	set, err := s.client.Do(ctx, s.client.B().Set().Key(s.decisionKey(id)).Value(string(raw)).Nx().PxMilliseconds(ttl).Build()).ToString()
	if valkey.IsValkeyNil(err) || (err == nil && set != "OK") {
		return api.ErrApprovalDecided
	}
	if err != nil {
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
