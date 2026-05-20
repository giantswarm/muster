// Package yaml implements agentgateway.Applier against the local filesystem:
// one agw native YAML file per MCPServer, written atomically into a configured
// directory. The Applier is long-lived (no per-MCPServer construction).
//
// The schema served is the v1.2.1 agentgateway native config; see
// local_types.go for the Go shape muster emits and the schema URL embedded
// as a yaml-language-server pragma header.
package yaml
