package aggregator

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"
	"github.com/giantswarm/mcp-oauth/requeststate"
	"github.com/giantswarm/mcp-oauth/security"
	"github.com/giantswarm/mcp-toolkit/urlelicitation"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// requestStateTTL is how long a requestState muster answers with stays
// valid: the sign-in link's OAuth state lives as long (signInElicitationTTL).
const requestStateTTL = signInElicitationTTL

// localCaller names the caller of a muster without inbound OAuth, which
// serves one person: requestState is bound to the session muster keys that
// person's state by.
const localCaller = "urn:muster:local"

// Kinds of state muster seals into a requestState.
const (
	stateKindSignIn  = "signIn"
	stateKindBackend = "backend"
)

// errInvalidRequestState is a requestState muster did not seal for this
// caller and call, or changed since.
var errInvalidRequestState = errors.New("invalid requestState")

// sealedRequestState is what muster seals into the requestState of an
// input_required answer: the sign-in it waits for (signInRequestState) or a
// backend's input request (backendInputState).
type sealedRequestState struct {
	Kind    string              `json:"kind"`
	SignIn  *signInRequestState `json:"signIn,omitempty"`
	Backend *backendInputState  `json:"backend,omitempty"`
}

// newRequestStateSealer returns the sealer of muster's requestState over the
// configured keys. Without one it seals with a key of this process alone and
// warns: a retry that reaches another replica is then refused.
func newRequestStateSealer(oauthServer config.OAuthServerConfig) (*requeststate.Sealer, error) {
	if oauthServer.RequestStateKey == "" {
		if oauthServer.RequestStatePreviousKey != "" {
			return nil, errors.New("requestStatePreviousKey is set without requestStateKey")
		}
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("requestState key: %w", err)
		}
		logging.Warn("Aggregator", "No requestStateKey configured: sealing requestState with a key of this process; with more than one replica, a retry reaching another replica is refused")
		return sealerOf(security.Key{ID: security.KeyID(secret), Secret: secret})
	}
	active, err := requestStateKey("requestStateKey", oauthServer.RequestStateKey)
	if err != nil {
		return nil, err
	}
	if oauthServer.RequestStatePreviousKey == "" {
		return sealerOf(active)
	}
	previous, err := requestStateKey("requestStatePreviousKey", oauthServer.RequestStatePreviousKey)
	if err != nil {
		return nil, err
	}
	if previous.ID == active.ID {
		return nil, errors.New("requestStatePreviousKey has the key ID of requestStateKey; generate a new key")
	}
	return sealerOf(active, previous)
}

func requestStateKey(name, encoded string) (security.Key, error) {
	secret, err := security.DecodeKey(encoded)
	if err != nil {
		return security.Key{}, fmt.Errorf("%s: %w", name, err)
	}
	return security.Key{ID: security.KeyID(secret), Secret: secret}, nil
}

func sealerOf(active security.Key, previous ...security.Key) (*requeststate.Sealer, error) {
	ring, err := security.NewKeyRing(active, previous...)
	if err != nil {
		return nil, fmt.Errorf("requestState key: %w", err)
	}
	return requeststate.New(ring)
}

// requestStateCaller returns the caller a requestState is bound to: the
// validated user of muster's inbound OAuth or, on a muster without it, the
// one local person in the session muster keys their state by. It returns nil
// when neither is known.
func requestStateCaller(ctx context.Context) *providers.UserInfo {
	if user, ok := oauthhandler.UserInfoFromContext(ctx); ok && user != nil {
		return user
	}
	if api.GetSubjectFromContext(ctx) != stdioDefaultUser {
		return nil
	}
	sessionID := getSessionIDFromContext(ctx)
	if sessionID == "" {
		return nil
	}
	return &providers.UserInfo{Issuer: localCaller, ID: sessionID, ClientID: localCaller}
}

// sealRequestState seals state for the caller and call. A caller it cannot
// be bound to (requeststate.ErrIncompleteIdentity, ErrMissingClientID) fails
// the call, as does a failure of the sealer: muster never answers a call
// that needs state without it.
func (a *AggregatorServer) sealRequestState(ctx context.Context, req mcp.CallToolRequest, state sealedRequestState) (string, error) {
	if a.requestStates == nil {
		return "", errors.New("no requestState sealer")
	}
	request, err := urlelicitation.RequestBinding(req)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("requestState: %w", err)
	}
	sealed, err := a.requestStates.Seal(requestStateCaller(ctx), request, payload, requestStateTTL)
	if err != nil {
		return "", fmt.Errorf("seal requestState: %w", err)
	}
	return sealed, nil
}

// openRequestState opens the requestState of a retry. It returns nil when the
// call is to be handled as a first call: no requestState, or one that lapsed.
// Any other requestState is refused with invalid params (-32602), as is one
// presented by a caller it cannot be bound to, as muster never sealed one for
// such a caller.
func (a *AggregatorServer) openRequestState(ctx context.Context, req mcp.CallToolRequest) (*sealedRequestState, error) {
	if req.Params.RequestState == "" {
		return nil, nil
	}
	if a.requestStates == nil {
		return nil, refuseRequestState(errInvalidRequestState)
	}
	request, err := urlelicitation.RequestBinding(req)
	if err != nil {
		return nil, refuseRequestState(fmt.Errorf("%w: %w", errInvalidRequestState, err))
	}
	payload, err := a.requestStates.Open(requestStateCaller(ctx), request, req.Params.RequestState)
	if errors.Is(err, requeststate.ErrExpired) {
		return nil, nil
	}
	if err != nil {
		return nil, refuseRequestState(fmt.Errorf("%w: %w", errInvalidRequestState, err))
	}
	var state sealedRequestState
	if err := json.Unmarshal(payload, &state); err != nil || !state.valid() {
		return nil, refuseRequestState(errInvalidRequestState)
	}
	return &state, nil
}

func (s sealedRequestState) valid() bool {
	switch s.Kind {
	case stateKindSignIn:
		return s.SignIn != nil && s.Backend == nil
	case stateKindBackend:
		return s.Backend != nil && s.SignIn == nil && s.Backend.Server != "" && s.Backend.Tool != ""
	default:
		return false
	}
}

type requestStateError struct{ err error }

func (e requestStateError) Error() string { return e.err.Error() }
func (e requestStateError) Unwrap() error { return e.err }

func (e requestStateError) JSONRPCError() mcp.JSONRPCError {
	return mcp.JSONRPCError{
		JSONRPC: mcp.JSONRPC_VERSION,
		Error:   mcp.JSONRPCErrorDetails{Code: mcp.INVALID_PARAMS, Message: e.Error()},
	}
}

// refuseRequestState marks err for mcp-go to answer as -32602 rather than as an
// internal error.
func refuseRequestState(err error) error {
	return mcpserver.NewHandlerError(requestStateError{err: err})
}
