# The problem muster solves

Platform engineers work with many systems: Kubernetes, Prometheus, Grafana, Flux, GitHub, cloud
APIs and the organisation's own tools. MCP servers exist for most of them, and an AI agent with
those servers attached can operate across all of them in one conversation. Attaching them
directly does not scale past a handful of servers.

## Tool definitions crowd out the work

An MCP client loads the definition of every tool of every attached server into the model's
context before the first question is asked. A single server contributes ten to fifty tools;
a platform team's set of servers contributes hundreds, and a fleet of clusters served by one
server each multiplies that again. The definitions alone can take more of the context window
than the task, on every turn, and the model picks tools less reliably the more it has to choose
from.

muster's answer is a discovery layer. A client sees thirteen meta-tools. `filter_tools` ranks
the catalogue against a query and returns a short, summarised page; `describe_tool` returns the
full schema of one tool; `call_tool` runs it. The catalogue can be hundreds of tools wide and
still cost a few hundred tokens per turn. A [toolset](../reference/toolsets.md) narrows it
further for a given agent: read-only tools, one server, one set of workflows.

## Every agent is its own integration

Without an aggregator, each client is configured with each server, each with its own
credentials, and every person repeats that setup. A remote server that requires login is logged
in to from every client separately. Nothing records who called what.

muster is one endpoint. Servers are registered once, as files or as Kubernetes resources, and
every client that connects gets the same catalogue. A person logs in once, to muster; muster
forwards that identity to the servers that accept it and handles the browser login for the
ones that run their own OAuth. Every tool call is made under the person's identity and appears
in muster's traces, metrics and logs.

## Procedures are rediscovered every time

Operational tasks are sequences: find the pods, read their logs, query the metric, compare.
Left to an agent, the sequence is rediscovered on every run, differently each time, at full
token cost.

A muster workflow turns the sequence into one tool with declared arguments, templated steps,
conditions, loops and a durable execution record. The agent calls `workflow_<name>`; the steps
run the same way every time and the result is one structured answer.

## Servers need running

MCP servers fail, restart, move and change their tool lists. A client with a direct connection
notices when a call fails.

muster owns the connections: it starts local servers, connects to remote ones, probes them,
reconnects with backoff, follows changes to their tool lists and reports all of it as status,
conditions and events on the `MCPServer` resource. Clients see a catalogue that reflects what is
reachable now.

## What this adds up to

One authenticated endpoint that presents many MCP servers as one catalogue, sized for a context
window, scoped per request, called under the person's identity, and operated like any other
platform service. [Architecture](architecture.md) describes how the pieces fit;
[MCP aggregation](mcp-aggregation.md) how the catalogue is built.
