package aggregator

import (
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

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

// requestStateKeyLabel separates the requestState key derived from muster's
// token encryption key from that key and from any other key derived from it.
const requestStateKeyLabel = "muster requestState v1"

// newRequestStateSealer returns the sealer of muster's requestState. Its key
// is derived from the token encryption key (HKDF-SHA256), which every replica
// shares, so a retry can reach any replica and no further secret is needed.
// Without an encryption key it seals with a key of this process alone and
// warns: a retry that reaches another replica is then refused.
func newRequestStateSealer(oauthServer config.OAuthServerConfig) (*requeststate.Sealer, error) {
	secret, err := requestStateSecret(oauthServer.EncryptionKey)
	if err != nil {
		return nil, err
	}
	ring, err := security.NewKeyRing(security.Key{ID: security.KeyID(secret), Secret: secret})
	if err != nil {
		return nil, fmt.Errorf("requestState key: %w", err)
	}
	return requeststate.New(ring)
}

func requestStateSecret(encryptionKey string) ([]byte, error) {
	if encryptionKey == "" {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("requestState key: %w", err)
		}
		logging.Warn("Aggregator", "No encryptionKey configured: sealing requestState with a key of this process; with more than one replica, a retry reaching another replica is refused")
		return secret, nil
	}
	key, err := security.DecodeKey(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("encryption key: %w", err)
	}
	if err := security.ValidateKeyEntropy(key); err != nil {
		return nil, fmt.Errorf("encryption key: %w", err)
	}
	secret, err := hkdf.Key(sha256.New, key, nil, requestStateKeyLabel, 32)
	if err != nil {
		return nil, fmt.Errorf("requestState key: %w", err)
	}
	return secret, nil
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
func (a *AggregatorServer) sealRequestState(ctx context.Context, request mcp.CallToolRequest, state sealedRequestState) (string, error) {
	if a.requestStates == nil {
		return "", errors.New("no requestState sealer")
	}
	binding, err := urlelicitation.RequestBinding(request)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("requestState: %w", err)
	}
	sealed, err := a.requestStates.Seal(requestStateCaller(ctx), binding, payload, requestStateTTL)
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
func (a *AggregatorServer) openRequestState(ctx context.Context, request mcp.CallToolRequest) (*sealedRequestState, error) {
	if request.Params.RequestState == "" {
		return nil, nil
	}
	reject := func(reason string, cause error) (*sealedRequestState, error) {
		logging.WarnWithAttrsCtx(ctx, "Aggregator", "Request state rejected",
			slog.String("event", "request_state_rejected"),
			slog.String("reason", reason),
			slog.String("tool", request.Params.Name),
			slog.Any("caller", requestStateCaller(ctx)))
		return nil, refuseRequestState(cause)
	}
	if a.requestStates == nil {
		return reject("sealer_unavailable", errInvalidRequestState)
	}
	binding, err := urlelicitation.RequestBinding(request)
	if err != nil {
		return reject("invalid_request", fmt.Errorf("%w: %w", errInvalidRequestState, err))
	}
	payload, err := a.requestStates.Open(requestStateCaller(ctx), binding, request.Params.RequestState)
	if errors.Is(err, requeststate.ErrExpired) {
		return nil, nil
	}
	if err != nil {
		return reject("verification_failed", fmt.Errorf("%w: %w", errInvalidRequestState, err))
	}
	var state sealedRequestState
	if err := json.Unmarshal(payload, &state); err != nil || !state.valid() {
		return reject("invalid_payload", errInvalidRequestState)
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
