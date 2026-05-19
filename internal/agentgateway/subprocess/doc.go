// Package subprocess supervises a single external process — used by
// filesystem-mode muster to run the agentgateway binary as a child.
//
// The Manager owns one process at a time:
//
//   - Start spawns the process, pipes stdout/stderr into a slog.Logger,
//     and blocks until a caller-supplied readiness probe succeeds.
//   - Stop sends SIGTERM, waits up to a configurable drain timeout, then
//     escalates to SIGKILL. The whole process group is signalled so any
//     children the process spawned (e.g. agentgateway's own stdio MCP
//     children) terminate with it.
//   - Reload sends SIGHUP. agentgateway watches its config directory
//     natively, so Reload is belt-and-suspenders.
//   - If the process exits unexpectedly the Manager restarts it with
//     capped exponential backoff.
//
// The package has no knowledge of MCPServer, agentgateway config, or
// muster's service locator. Wiring into cmd/serve.go lives elsewhere.
package subprocess
