# AI Agent Troubleshooting Guide

Diagnose and resolve AI agent issues when using Muster for infrastructure automation.

> This guide uses only commands and configuration that Muster actually
> implements. The full CLI is `muster serve | agent | standalone | list | get |
> check | call | create | auth | context | events | test | version |
> self-update | start | stop`. There is no `muster configure`, `muster cache`,
> `muster status`, `muster restart`, `muster logs`, `muster metrics`, or
> `muster diagnostics` command. Configuration is file-based.

## Quick Diagnostics

```bash
# Is the binary working and which version?
muster version

# Is the aggregator reachable? (default endpoint http://localhost:8090/mcp)
curl -fsS http://localhost:8090/health && echo OK

# What does the aggregator expose?
muster list mcpserver
muster list tools

# Authentication state (for remote/protected aggregators)
muster auth status
```

`muster list`, `muster check`, `muster call`, and `muster get` all require a
running aggregator (`muster serve`). They connect to the endpoint from your
config or `--endpoint` / `MUSTER_ENDPOINT`.

## Common Issues and Solutions

### Agent Connection Problems

**Symptoms:** the AI agent shows no tools, "connection timeout", or "MCP server not found".

**Diagnose:**
```bash
# 1. Is a muster process running?
ps aux | grep '[m]uster'

# 2. Does the binary run?
muster version

# 3. Is the aggregator answering on its port?
curl -fsS http://localhost:8090/health

# 4. Check your IDE's MCP configuration
cat ~/.cursor/mcp.json | jq '.mcpServers'   # Cursor
```

**Fix 1 — start (or restart) the server:**
```bash
# Standalone mode runs the aggregator and the stdio agent in one process
muster standalone

# Or run the aggregator on its own
muster serve
```
muster has no `restart` subcommand — stop the existing process (Ctrl+C, or
`muster stop`) and start it again, or restart your service manager unit.

**Fix 2 — reset configuration:**

Configuration is a file, not a CLI. The defaults live in
`~/.config/muster/config.yaml` (or `.muster/config.yaml` for a project, or the
directory passed to `--config-path`). To reset, move it aside so muster
recreates defaults:
```bash
mv ~/.config/muster/config.yaml ~/.config/muster/config.yaml.backup
```

**Fix 3 — check the port:**
```bash
# Is something listening on the aggregator port?
ss -tlnp | grep 8090

curl -v http://localhost:8090/health
```

### Authentication Problems

**Symptoms:** tools are visible but execution fails with "authentication required", 401/Unauthorized, or the IDE reports the muster server as needing login.

Muster CLI commands return exit code `2` when authentication is required and `3`
when an OAuth flow fails (`0` success, `1` general error).

**Diagnose:**
```bash
muster auth status                 # all known endpoints + per-MCP-server SSO state
muster auth status --server <name> # one downstream MCP server
muster auth whoami                 # current identity, issuer, token expiry
```

**Fix — (re)authenticate:**
```bash
muster auth login                       # configured aggregator
muster auth login --endpoint <url>      # a specific remote endpoint
muster auth login --server <name>       # a downstream MCP server that needs SSO

# Clear tokens and start over
muster auth logout            # configured aggregator
muster auth logout --all      # every stored token
```

Downstream MCP servers usually authenticate via SSO (token forwarding or RFC 8693
token exchange) off your muster login — `muster auth status` shows `[SSO: Forwarded]`
or `[SSO: Exchanged]`. If it shows `[SSO: Failed]`, the downstream server's OAuth
configuration is the thing to check, not your login.

### Tool Discovery and Execution Issues

**Symptoms:** expected tools are missing, or a tool is found but fails.

**Diagnose:**
```bash
# What is actually aggregated, and from which server?
muster list tools
muster list tools --server github          # filter by server prefix
muster list tools --filter "*deploy*"      # filter by name pattern
muster list mcpserver --all --verbose      # include unreachable servers + errors

# Is a specific MCP server / workflow available?
muster check mcpserver kubernetes
muster check workflow deploy-webapp

# Run a tool directly to see the raw result
muster call core_service_list
muster call core_service_status --name=prometheus
muster call workflow_deploy_webapp --json '{"app_name":"test","environment":"development"}'
```

**Fixes:**
- A missing MCP server: inspect it with `muster get mcpserver <name>` and check
  its definition under `{config-path}/mcpservers/`. A server that fails to start
  shows up in `muster list mcpserver --all --verbose` with its error.
- Tools missing because the backing server is down: fix the server command/URL in
  its definition and restart `muster serve`.
- A workflow reported unavailable: `muster check workflow <name>` lists the tools
  it needs; an unavailable workflow is missing at least one of them.

### Performance Issues

**Symptoms:** slow agent responses, high CPU/memory.

**Diagnose:**
```bash
# System view of the muster process
top -p "$(pgrep -d, muster)"
free -h
```

Muster emits logs, traces, and metrics via OpenTelemetry (OTLP). Point the
standard `OTEL_EXPORTER_OTLP_*` environment variables at your collector to get
real metrics and traces; there is no `muster metrics` or `muster profile`
command. To silence console logs while keeping OTLP, run `muster serve --silent`.

**Reduce agent context size** in your AI assistant's settings (number of files,
lines per file, exclude patterns) — this is configured in the assistant, not in
muster.

### Configuration Issues

**Symptoms:** muster fails to start, or behaves unexpectedly after a config change.

Configuration is YAML on disk. There is no config-validation subcommand; muster
reports configuration errors on startup. To bisect a bad change:
```bash
# Move the current config aside (keeping a copy) to fall back to defaults
mv ~/.config/muster/config.yaml ~/.config/muster/config.yaml.broken

# Add entities back one at a time and check after each
muster create mcpserver kubernetes --command "..."
muster check mcpserver kubernetes
```
Entity definitions live under `{config-path}/{mcpservers,workflows}/` and can
also be managed as Kubernetes CRDs (`kubectl get mcpservers,workflows`).

### Wrong-Environment / Context Confusion

**Symptoms:** actions hit the wrong cluster/environment.

Muster selects the target aggregator via *contexts*:
```bash
muster context list        # available contexts
muster context current     # the active one
muster context use <name>  # switch
```
You can also pin a context per command with `--context <name>` or the
`MUSTER_CONTEXT` environment variable, and target an explicit endpoint with
`--endpoint <url>` / `MUSTER_ENDPOINT`.

## Debugging Tools and Techniques

```bash
# Verbose aggregator logs on stderr
muster serve --debug

# Capture them to a file
muster serve --debug > /tmp/muster-debug.log 2>&1

# Inspect MCP protocol traffic from the client side
muster agent --repl --json-rpc      # interactive REPL with raw JSON-RPC logging
muster agent --verbose              # show keepalives and connection detail

# CLI commands that connect to the aggregator accept --debug too
muster list tools --debug
```

For the stdio agent used by IDEs:
```bash
muster agent --mcp-server   # what your IDE launches; logs are suppressed in this mode
```

## Getting Additional Help

```bash
muster --help
muster <command> --help     # e.g. muster auth --help, muster list --help
```

- **GitHub Issues**: [Report bugs and get help](https://github.com/giantswarm/muster/issues)
- **Discussions**: [Community forum](https://github.com/giantswarm/muster/discussions)

## Related Documentation

- [AI Agent Integration Guide](ai-agent-integration.md)
- [Workflow Creation](workflow-creation.md)
- [General Troubleshooting](troubleshooting.md)
