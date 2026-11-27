# Backend OAuth reference proof

Run the reference MCP server and stand-in authorization servers through the
existing integration harness:

```bash
go build -o ./muster .
./muster test --scenario external-app-backend-oauth --parallel 1 --base-port 45100
```

The scenario signs two people into muster, completes backend onboarding for one
of them using the existing backend OAuth flow, and calls a protected operation.
The backend reports its independently authenticated user, not the platform user
or an application-supplied session reference. The other person cannot use that
grant. A retry retains the backend identity. `echo_authorization` reports verified
subject, issuer, client and scope information without returning the bearer token.
The mock fixtures' unit test covers both opaque access tokens and signed JWTs.
The regular `muster-integration-test` target runs this scenario in CI.

Customer MCP servers publish their protected-resource metadata and authorization
server endpoints, or use an operator-pinned authorization server and an existing
pre-registered client. Configure `auth.type: oauth` with `forwardToken: false`.
Use `authorizationServer.grantScope: subject` when onboarding should carry across
the same person's later sessions. Existing resource RBAC controls discovery and
invocation, including explicitly shared resources across organizations. The
backend applies its own permissions to its authenticated user.

Application credentials identify an application. Request-scoped metadata may
carry a session reference, but it is neither a credential nor proof of an end
user's authority. Credentials belong in the authorization header, outside model
requests. Never forward the platform bearer token to a customer backend or add
customer-specific token logic to muster.

This proof covers the backend OAuth hop. The complete agentgateway, Dex,
klaus-gateway, agent-manager and kagent path still needs the agentlab proof,
including streaming, cards, token refusals, metadata isolation and RBAC revocation.
A customer operation and independent security sign-off remain acceptance and
rollout gates.
