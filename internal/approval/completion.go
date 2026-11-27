package approval

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/valkey-io/valkey-go"
)

// Kinds of completion notice.
const (
	// CompletionApproval says an approval request was decided; Key is the
	// request id.
	CompletionApproval = "approval"
	// CompletionSignIn says a session's sign-in to a server ended; Key is
	// the digest of the session id and the server (SignInKey).
	CompletionSignIn = "sign_in"
)

// Completion is the notice that an elicitation ended (the person decided on
// the approval page, or the sign-in's callback ran). It carries only what
// names the elicitation: never a decision, a person, or a session id. The
// replica that holds the transport session which was asked, if any, looks its
// target up locally and sends notifications/elicitation/complete to it.
type Completion struct {
	Kind string `json:"k"`
	Key  string `json:"key"`
	// ElicitationID is the approval request's elicitationId; empty for a
	// sign-in, whose elicitations are per transport session.
	ElicitationID string `json:"e,omitempty"`
}

// SignInKey is the Key of a sign-in Completion: a digest, so the muster
// session id never leaves the replicas' memory.
func SignInKey(sessionID, server string) string {
	return digest(sessionID + "\x00" + server)
}

// Completions carries Completion notices between the replicas. Delivery is
// best effort and may repeat: the client treats elicitation/complete as
// idempotent, and the receivers forget what they delivered.
type Completions interface {
	// Publish sends the notice to every subscribed replica, this one
	// included.
	Publish(ctx context.Context, c Completion) error
	// Subscribe calls handle for every notice until ctx ends (returns nil)
	// or the subscription is lost (returns the error; the caller may
	// subscribe again).
	Subscribe(ctx context.Context, handle func(Completion)) error
}

// MemoryCompletions delivers notices inside the process: the bus of one
// replica, and of tests that stand for several.
type MemoryCompletions struct {
	mu       sync.Mutex
	handlers map[int]func(Completion)
	next     int
}

var _ Completions = (*MemoryCompletions)(nil)

// NewMemoryCompletions returns an empty in-process bus.
func NewMemoryCompletions() *MemoryCompletions {
	return &MemoryCompletions{handlers: map[int]func(Completion){}}
}

// Subscribers returns how many subscriptions are live.
func (m *MemoryCompletions) Subscribers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.handlers)
}

// Publish implements Completions.
func (m *MemoryCompletions) Publish(_ context.Context, c Completion) error {
	m.mu.Lock()
	handlers := make([]func(Completion), 0, len(m.handlers))
	for _, h := range m.handlers {
		handlers = append(handlers, h)
	}
	m.mu.Unlock()
	for _, h := range handlers {
		h(c)
	}
	return nil
}

// Subscribe implements Completions.
func (m *MemoryCompletions) Subscribe(ctx context.Context, handle func(Completion)) error {
	m.mu.Lock()
	id := m.next
	m.next++
	m.handlers[id] = handle
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.handlers, id)
		m.mu.Unlock()
	}()
	<-ctx.Done()
	return nil
}

// ValkeyCompletions carries notices on a Valkey pub/sub channel, so a
// decision or sign-in served by one replica reaches the replica that holds
// the asking session.
type ValkeyCompletions struct {
	client  valkey.Client
	channel string
}

var _ Completions = (*ValkeyCompletions)(nil)

// NewValkeyCompletions returns a bus on client under keyPrefix.
func NewValkeyCompletions(client valkey.Client, keyPrefix string) *ValkeyCompletions {
	return &ValkeyCompletions{client: client, channel: CompletionChannel(keyPrefix)}
}

// CompletionChannel is the pub/sub channel of the notices under keyPrefix.
func CompletionChannel(keyPrefix string) string {
	return keyPrefix + "approval:{a}:completed"
}

// Publish implements Completions.
func (v *ValkeyCompletions) Publish(ctx context.Context, c Completion) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return v.client.Do(ctx, v.client.B().Publish().Channel(v.channel).Message(string(raw)).Build()).Error()
}

// Subscribe implements Completions. A message that is not a notice is
// dropped.
func (v *ValkeyCompletions) Subscribe(ctx context.Context, handle func(Completion)) error {
	// A connection of its own: a subscribed connection takes no other command.
	dedicated, release := v.client.Dedicate()
	defer release()
	err := dedicated.Receive(ctx, v.client.B().Subscribe().Channel(v.channel).Build(), func(msg valkey.PubSubMessage) {
		var c Completion
		if json.Unmarshal([]byte(msg.Message), &c) == nil && c.Key != "" {
			handle(c)
		}
	})
	if ctx.Err() != nil {
		return nil
	}
	return err
}
