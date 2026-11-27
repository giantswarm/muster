// Package approval keeps the pending approvals of calls whose effective
// approval is Ask and serves muster's approval page.
//
// A call that asks is recorded as an api.ApprovalRequest, bound to the
// person (issuer and raw sub) and Session it came from, the resolved target
// and the RFC 8785 canonical form of the arguments muster dispatches
// (Canonical). The request lives in muster's shared store (ValkeyStore), so
// every replica sees it, expires after minutes and is consumed once.
//
// The page (Page) shows the call in plain words worked out from the tool's
// own definition and takes the decision only from a browser signed in, through
// muster's own identity provider, as the person the request is bound to; a
// bearer or actor token never decides. The decision is a POST carrying an
// anti-forgery token bound to the browser; afterwards the browser is sent
// back (303) to the return its client asked for, when that return is on the
// operator's allowlist, with the elicitation's id appended and never the
// decision.
package approval
