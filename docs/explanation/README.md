# Explanation

How muster works and why it is built the way it is. These pages are background reading; the
[how-to guides](../how-to/README.md) tell you what to type.

## Concepts

- [The problem muster solves](problem-statement.md): why one endpoint and a discovery layer, rather than every MCP server in every agent.
- [Architecture](architecture.md): the aggregator, the meta-tools, sessions, the service locator that holds the code together.
- [MCP aggregation](mcp-aggregation.md): registration, naming, families, tool visibility per session, execution.
- [Service orchestration and workflows](orchestration.md): the lifecycle of a registered server and how workflows execute.
- [Observability](observability.md): what muster contributes to a trace, the metrics it exports and the structure of its logs.
- [Design principles](design-principles.md): the rules the codebase follows.

## Components

- [Aggregator](components/aggregator.md): registry, tool factory, event handler and the stores behind a session.
- [Workflows](components/workflows.md): definitions, execution engine and templating.

## Decisions

The [architecture decision records](decisions/README.md) capture the choices that shaped
muster: the service locator, OAuth in front of muster and towards remote servers,
session-scoped tool visibility, single sign-on by token forwarding, server-side meta-tools.

## Protocol analysis

[MCP 2026-07-28](mcp-2026-07-28/README.md) is muster's section-by-section analysis of the
protocol revision that removes the session handshake and makes extensions first class: what
each change means for an aggregator, what has to change, and what is still open.
