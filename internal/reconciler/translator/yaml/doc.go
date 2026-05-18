// Package yaml is a translator.ConfigEmitter that persists each MCPServer's
// agentgateway configuration as a standalone native YAML file inside a
// configured directory.
//
// File layout: one file per MCPServer, named <mcpserver-name>.yaml. Each
// file is a complete agentgateway LocalConfig with a single bind, listener,
// route and MCP backend. The first line is a yaml-language-server pragma
// pinning the v1.2.1 schema.
//
// Atomicity: writes go through <name>.yaml.tmp, are fsynced, then renamed
// over the target. Renames are atomic on POSIX, so readers never observe a
// half-written file. Distinct names are written in parallel; the same name
// is serialized.
//
// Idempotency: emitting the same Model twice produces byte-identical
// output, and a re-emit short-circuits when the on-disk bytes already
// match.
//
// Containment: file operations go through an os.Root opened on the
// configured directory; the emitter cannot read or write outside that
// subtree even when given a crafted name.
package yaml
