# How-to guides

Each guide solves one task. They assume a running muster; the
[quick start](../getting-started/quick-start.md) gets you there.

## Clients and access

- [Connect MCP clients](connect-mcp-clients.md): Cursor, VS Code, Claude Code, Claude Desktop, and any client that speaks MCP over HTTP, locally and against a remote muster.
- [Authenticate the CLI](authenticate-the-cli.md): logging in to a protected muster, contexts, authentication modes, tokens and exit codes.

## MCP servers

- [Manage MCP servers](mcp-server-management.md): register `stdio` and remote servers, auto-start, health, SSO, SigV4 signing and request metadata.
- [Connect servers without RFC 9728 metadata](connecting-non-rfc9728-mcp-servers.md): pin the authorization server of a remote MCP server that does not advertise it, including GitHub's hosted server.
- [Integrate with Kubernetes](kubernetes-integration.md): register mcp-kubernetes with the person's identity forwarded, across one or many clusters.

## Workflows

- [Create workflows](workflow-creation.md): arguments, templating, conditions, loops, parallel steps and error handling.
- [Optimise workflows](ai-workflow-optimization.md): structure a workflow so it runs fast and fails clearly.
- [Advanced scenarios](advanced-scenarios.md): longer, real-world workflow examples.

## Operations

- [Monitor servers and workflows](monitoring-setup.md): health checks, events and the CLI commands that inspect them.
- [Troubleshooting](troubleshooting.md): tools that do not appear, workflows that fail, servers that do not start, connectivity.
- [Troubleshooting AI agents](ai-troubleshooting.md): when the agent, not muster, is the one misbehaving.
