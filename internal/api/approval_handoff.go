package api

import (
	"context"
	"sync"
)

// A sign-in handoff lets a client the operator trusts with it (the portal)
// spare a person who is signed in there muster's own browser sign-in on one
// page: the client asks muster, with the person's bearer, for a one-time
// ticket bound to that page and that person, and adds it to the link it
// opens. muster's page redeems it once, within a minute, and treats the
// browser as signed in as that person for that page only. The page's other
// checks (anti-forgery token, Origin, the person the request is bound to)
// stay as they are.

// The pages a handoff is bound to.
const (
	// HandoffKindApproval is the approval page of one request; the target
	// is the request's id.
	HandoffKindApproval = "approval"
	// HandoffKindSignIn is the OAuth proxy start of one connector sign-in;
	// the target is the digest of its state.
	HandoffKindSignIn = "sign_in"
)

// SignInHandoff is what muster keeps of a ticket: the page it is bound to and
// the person it was issued to.
type SignInHandoff struct {
	Kind     string         `json:"kind"`
	Target   string         `json:"target"`
	Identity SignInIdentity `json:"identity"`
	// Name is how the person is named for people by a verified identifier
	// (the verified email, else subject at issuer, marked for invisible and
	// bidi characters), from the client's bearer when the ticket was issued;
	// shown on the connector sign-in's confirmation.
	Name string `json:"name,omitempty"`
}

// HandoffRedeemer redeems a ticket for a page: the handoff and true when the
// ticket exists, is unused and unexpired, and is bound to kind and target and
// to the person want. The ticket is used up whatever the answer.
type HandoffRedeemer func(ctx context.Context, ticket, kind, target string, want SignInIdentity) (SignInHandoff, bool)

var (
	handoffRedeemer      HandoffRedeemer
	handoffRedeemerMutex sync.RWMutex
)

// RegisterHandoffRedeemer registers the redeemer; nil unregisters it.
func RegisterHandoffRedeemer(r HandoffRedeemer) {
	handoffRedeemerMutex.Lock()
	defer handoffRedeemerMutex.Unlock()
	handoffRedeemer = r
}

// RedeemHandoff redeems a ticket with the registered redeemer; false
// without one.
func RedeemHandoff(ctx context.Context, ticket, kind, target string, want SignInIdentity) (SignInHandoff, bool) {
	handoffRedeemerMutex.RLock()
	r := handoffRedeemer
	handoffRedeemerMutex.RUnlock()
	if r == nil {
		return SignInHandoff{}, false
	}
	return r(ctx, ticket, kind, target, want)
}
