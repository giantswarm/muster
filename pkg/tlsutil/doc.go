// Package tlsutil provides small helpers for building outbound TLS trust from
// operator-provided CA files.
//
// It exists so the process-wide --extra-ca-file trust (installed on
// http.DefaultTransport at startup) and the explicit per-client CA pools that
// mcp-oauth's permissive JWKS / token-exchange clients now require are built
// from one place and therefore verify against an identical pool (system roots
// plus the operator's extra CAs).
package tlsutil
