# Components

Deep dives into the packages that carry most of muster's behaviour. The map of all packages and
how they connect is in [Architecture](../architecture.md).

- [Aggregator](aggregator.md): `internal/aggregator`, the registry of servers and tools, the tool factory that wraps downstream tools, the event handler that follows server state, and the per-session stores.
- [Workflows](workflows.md): `internal/workflow`, workflow definitions, the execution engine and the template language.
