package aggregator

import "time"

// httpReadHeaderTimeout caps how long the HTTP servers wait for request
// headers. Bounds Slowloris-style request-header attacks.
const httpReadHeaderTimeout = 30 * time.Second

// maxRequestBodyBytes bounds a request body the MCP transports read
// (CVE-2026-108859: mcp-go read a POST body whole before validating it). The
// aggregator relays every backend's tool calls, so it is no tighter than the
// largest backend bound: vm-manager's 8 MiB, which a VM's user_data needs.
const maxRequestBodyBytes = 8 << 20

// registerCapabilityAttemptTimeout bounds a single round of initial capability
// queries (tools/resources/prompts) against a freshly registered downstream
// server. Some transports can wedge on an individual request — an SSE server
// may accept tools/list but never deliver the response event — and without a
// bound the registration goroutine would hang forever (#1113).
//
// Variables rather than constants so tests can shrink them.
var registerCapabilityAttemptTimeout = 5 * time.Second

// registerCapabilityAttempts is how many bounded rounds of initial capability
// queries are made before the server is registered with whatever was fetched.
// A retry issues a fresh request, which recovers from per-request wedges that
// a longer wait on the first request would not.
var registerCapabilityAttempts = 3
