# muster documentation

muster is an aggregating Model Context Protocol server: it connects to the MCP servers a
platform team runs and serves their tools through one authenticated endpoint, with discovery
that fits an agent's context window, per-request toolsets, single sign-on to the servers behind
it and a Kubernetes-native way to run all of this.

New to muster? Start with the [quick start](getting-started/quick-start.md); it takes about ten
minutes from an empty machine to an IDE calling tools through muster.

## Sections

The documentation follows the [Diátaxis](https://diataxis.fr/) structure: tutorials to learn by
doing, how-to guides for specific tasks, reference for exact facts, and explanation for the
reasoning behind the design.

| Section | Read it when you |
|---|---|
| [Getting started](getting-started/README.md) | want a working muster and your first aggregated tools, step by step |
| [How-to guides](how-to/README.md) | have a task: connect a client, authenticate, register a server, integrate with Kubernetes, build a workflow, fix a problem |
| [Reference](reference/README.md) | need the exact tool arguments, configuration keys, custom resource fields, events, HTTP endpoints or CLI flags |
| [Explanation](explanation/README.md) | want to understand how aggregation, sessions, workflows and observability work, and why they work that way |
| [Operations](operations/README.md) | install muster for a team and keep it secure |
| [Contributing](contributing/README.md) | change muster itself |

## By task

- Connect Cursor, VS Code, Claude Code or Claude Desktop: [Connect MCP clients](how-to/connect-mcp-clients.md)
- Use the CLI against a muster that requires login: [Authenticate the CLI](how-to/authenticate-the-cli.md)
- Register, update and troubleshoot MCP servers: [Manage MCP servers](how-to/mcp-server-management.md)
- Give an agent a bounded tool surface: [Toolsets](reference/toolsets.md)
- Run muster on a cluster with Dex and Valkey: [Installation](operations/installation.md)
- Turn a procedure into a deterministic tool: [Create workflows](how-to/workflow-creation.md)
- See what an agent sees: [MCP tools](reference/mcp-tools.md)

## Questions and problems

Bugs, gaps in the documentation and feature requests go to the
[issue tracker](https://github.com/giantswarm/muster/issues). Security issues are reported
through Giant Swarm's [responsible disclosure process](https://www.giantswarm.io/responsible-disclosure).
