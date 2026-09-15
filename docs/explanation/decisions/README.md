# Architecture decision records

Each record captures one decision that shaped muster: the situation, the choice, the
alternatives and the consequences. Records are numbered in the order they were made and are
not rewritten when superseded; a later record says what replaced them.

| Record | Decision |
|---|---|
| [001](001-api-service-locator.md) | Packages communicate only through the `internal/api` service locator |
| [002](002-testing-framework.md) | Behaviour is specified by BDD scenarios against real muster instances |
| [003](003-configuration-management.md) | Definitions are YAML files locally and custom resources on Kubernetes, with one schema |
| [004](004-oauth-proxy.md) | muster logs a person in to remote MCP servers that run their own OAuth |
| [005](005-muster-auth.md) | muster protects its own endpoint as an OAuth 2.1 resource server in front of Dex |
| [006](006-session-scoped-tool-visibility.md) | Tools of OAuth-protected servers are visible per session, after that session's login |
| [007](007-crd-status-reconciliation.md) | The reconciler owns `MCPServer` status and the aggregator follows it |
| [008](008-unified-authentication.md) | Authentication state is explicit: a server is authenticated, pending or not required |
| [009](009-sso-token-forwarding.md) | Single sign-on by forwarding the person's identity token to servers that trust the same issuer |
| [010](010-server-side-meta-tools.md) | The meta-tools live in the server; clients see only them |
| [011](011-session-connection-pool.md) | Downstream connections are pooled per session; authentication and capabilities are separate stores |

## Writing a record

A new record is the next number, named `NNN-<slug>.md`, with the sections *Context*,
*Decision*, *Alternatives considered* and *Consequences*. It is proposed in the pull request
that implements the decision and linked from this table.
