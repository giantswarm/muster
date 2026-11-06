package mcpserver

import (
	"github.com/giantswarm/muster/v5/internal/api"
)

// mcpServerArgs returns the common argument metadata for MCP server tools.
// typeRequired controls whether the "type" field is required (true for create/validate, false for update).
func mcpServerArgs(typeRequired bool) []api.ArgMetadata {
	tags := api.TagsUpdateArg()
	if typeRequired {
		tags = api.TagsArg()
	}
	return []api.ArgMetadata{
		{Name: "name", Type: api.ArgTypeString, Required: true, Description: "MCP server name"},
		{Name: "type", Type: api.ArgTypeString, Required: typeRequired, Description: "MCP server type (stdio, streamable-http, or sse)"},
		{Name: "toolPrefix", Type: api.ArgTypeString, Required: false, Description: "Tool prefix for namespacing"},
		{Name: "family", Type: api.ArgTypeObject, Required: false, Description: "Family that this MCP server instance belongs to (groups equivalent servers under a single tool name)", Schema: map[string]interface{}{
			api.SchemaKeyType:        string(api.ArgTypeObject),
			api.SchemaKeyDescription: "Family grouping for equivalent MCP server instances. When set, both name and instanceArg are required.",
			api.SchemaKeyProperties: map[string]interface{}{
				"name": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeString),
					api.SchemaKeyDescription: "Family identifier shared across instances",
				},
				"instanceArg": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeString),
					api.SchemaKeyDescription: "Name of the required parameter the LLM uses to select which family member handles a call (e.g. management_cluster, country, model)",
				},
			},
			api.SchemaKeyRequired: []string{"name", "instanceArg"},
		}},
		{Name: "displayName", Type: api.ArgTypeString, Required: false, Description: "Name UIs show for the server, stored trimmed in the ui.giantswarm.io/display-name annotation; blank sets none, on update removes it"},
		tags,
		{Name: "description", Type: api.ArgTypeString, Required: false, Description: "MCP server description"},
		{Name: "autoStart", Type: api.ArgTypeBoolean, Required: false, Description: "Whether server should auto-start; on update, omitted keeps the current value"},
		{Name: "command", Type: api.ArgTypeString, Required: false, Description: "Command executable path (required for stdio)"},
		{Name: "args", Type: api.ArgTypeArray, Required: false, Description: "Command arguments (stdio only)", Schema: map[string]interface{}{
			api.SchemaKeyType:        string(api.ArgTypeArray),
			api.SchemaKeyItems:       map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString)},
			api.SchemaKeyDescription: "Command line arguments for stdio servers",
		}},
		{Name: "url", Type: api.ArgTypeString, Required: false, Description: "Server endpoint URL (required for streamable-http and sse)"},
		{Name: "env", Type: api.ArgTypeObject, Required: false, Description: "Environment variables (stdio only)", Schema: map[string]interface{}{
			api.SchemaKeyType:                 string(api.ArgTypeObject),
			api.SchemaKeyAdditionalProperties: map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString)},
			api.SchemaKeyDescription:          "Environment variables for the server",
		}},
		{Name: "headers", Type: api.ArgTypeObject, Required: false, Description: "HTTP headers (streamable-http, sse only); a credential in Authorization, Proxy-Authorization, Cookie or X-API-Key is refused: use headersFrom", Schema: map[string]interface{}{
			api.SchemaKeyType:                 string(api.ArgTypeObject),
			api.SchemaKeyAdditionalProperties: map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString)},
			api.SchemaKeyDescription:          "HTTP headers for remote servers",
		}},
		{Name: "headersFrom", Type: api.ArgTypeArray, Required: false, Description: "HTTP headers whose values muster reads from a Secret in the server's namespace when it connects (streamable-http and sse only)", Schema: map[string]interface{}{
			api.SchemaKeyType: string(api.ArgTypeArray),
			api.SchemaKeyItems: map[string]interface{}{
				api.SchemaKeyType: string(api.ArgTypeObject),
				api.SchemaKeyProperties: map[string]interface{}{
					"name": map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString), api.SchemaKeyDescription: "HTTP header name"},
					"valueFrom": map[string]interface{}{
						api.SchemaKeyType: string(api.ArgTypeObject),
						api.SchemaKeyProperties: map[string]interface{}{
							"type": map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString), api.SchemaKeyEnum: []string{api.ValueSourceTypeSecret}},
							"name": map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString), api.SchemaKeyDescription: "Secret name"},
							"key":  map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString), api.SchemaKeyDescription: "Key in the Secret's data"},
						},
						api.SchemaKeyRequired: []string{"type", "name", "key"},
					},
				},
				api.SchemaKeyRequired: []string{"name", "valueFrom"},
			},
			api.SchemaKeyDescription: "Secret references for header values, such as a shared token",
		}},
		{Name: "meta", Type: api.ArgTypeObject, Required: false, Description: "Entries merged into the params._meta object of every outbound JSON-RPC request that carries params (applied by the sigv4 transport only)", Schema: map[string]interface{}{
			api.SchemaKeyType:                 string(api.ArgTypeObject),
			api.SchemaKeyAdditionalProperties: map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString)},
			api.SchemaKeyDescription:          "Request metadata passthrough, for backends that read call-scoped configuration from params._meta instead of from tool arguments. A value already present in a request wins. Injection happens in the sigv4 signing transport, so it takes effect only when auth.type is sigv4.",
		}},
		{Name: "timeout", Type: api.ArgTypeInteger, Required: false, Description: "Connection timeout in seconds"},
		{Name: "progress", Type: api.ArgTypeObject, Required: false, Description: "How progress the server reports on a tool call affects that call's budget", Schema: map[string]interface{}{
			api.SchemaKeyType:        string(api.ArgTypeObject),
			api.SchemaKeyDescription: "Progress configuration",
			api.SchemaKeyProperties: map[string]interface{}{
				"maxDuration": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeInteger),
					api.SchemaKeyDescription: "Seconds a tool call reporting progress may run in all; each notification restarts timeout up to it (timeout to 3600, default 3600)",
				},
			},
		}},
		{Name: "auth", Type: api.ArgTypeObject, Required: false, Description: "Authentication configuration for remote servers", Schema: map[string]interface{}{
			api.SchemaKeyType:        string(api.ArgTypeObject),
			api.SchemaKeyDescription: "Authentication configuration (oauth, none or sigv4)",
			api.SchemaKeyProperties: map[string]interface{}{
				api.SchemaKeyType: map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeString),
					api.SchemaKeyDescription: "Authentication type: oauth, none or sigv4",
					api.SchemaKeyEnum:        []string{"oauth", "none", api.MCPServerAuthTypeSigV4},
				},
				"sigv4": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeObject),
					api.SchemaKeyDescription: "AWS SigV4 request signing with muster's own machine identity; required when type is sigv4, rejected otherwise; streamable-http only",
					api.SchemaKeyProperties: map[string]interface{}{
						"region": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "SigV4 signing region; must match the region in the server URL",
						},
						"service": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "SigV4 signing service name; defaults to the first hostname label of the server URL",
						},
						"roleArn": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "IAM role assumed from the pod's base credentials before signing; empty signs as the pod's own identity",
						},
					},
					api.SchemaKeyRequired: []string{"region"},
				},
				"grantScope": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeString),
					api.SchemaKeyDescription: "Whose grant, for a discovered or pinned issuer (oauth): session (default) or subject (the person's; later sessions reuse it until core_auth_logout)",
					api.SchemaKeyEnum:        []string{api.GrantScopeSession, api.GrantScopeSubject},
				},
				"forwardToken": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeBoolean),
					api.SchemaKeyDescription: "Enable SSO token forwarding (oauth only)",
				},
				"forwardIdentity": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeBoolean),
					api.SchemaKeyDescription: "Send the session's ID token in X-Muster-Id-Token next to the pinned grant (needs oauth and authorizationServer); not audience-scoped to the server",
				},
				"requiredAudiences": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeArray),
					api.SchemaKeyItems:       map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString)},
					api.SchemaKeyDescription: "Additional audiences to request from IdP for token or identity forwarding (e.g., dex-k8s-authenticator for Kubernetes OIDC)",
				},
				"authorizationServer": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeObject),
					api.SchemaKeyDescription: "Pins the OAuth authorization server where the MCP server has no RFC 9728 metadata; with endpoints and a client, one muster can't discover (GitHub)",
					api.SchemaKeyProperties: map[string]interface{}{
						"issuer": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "OAuth/OIDC issuer URL (HTTPS, no trailing slash) the sign-in runs against and grants are filed under; wins over the one RFC 9728 metadata names",
						},
						"scopes": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "OAuth scope parameter value (space-separated scope tokens)",
						},
						"authorizationEndpoint": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "Authorization endpoint of a server without RFC 8414 discovery; set with tokenEndpoint (muster then does no discovery and assumes S256 PKCE)",
						},
						"tokenEndpoint": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "Token endpoint of an authorization server without an RFC 8414 discovery document; set together with authorizationEndpoint",
						},
						"expectedIssuer": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "The RFC 9207 iss of authorization responses where it differs from issuer, which stays the grant key; needs both endpoints; default: issuer",
						},
						"clientCredentialsSecretRef": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeObject),
							api.SchemaKeyDescription: "Secret holding a client registered out of band (a GitHub App or OAuth App); used instead of muster's CIMD URL or a dynamic registration",
							api.SchemaKeyProperties: map[string]interface{}{
								"name": map[string]interface{}{
									api.SchemaKeyType:        string(api.ArgTypeString),
									api.SchemaKeyDescription: "Name of the Kubernetes Secret",
								},
								"namespace": map[string]interface{}{
									api.SchemaKeyType:        string(api.ArgTypeString),
									api.SchemaKeyDescription: "Namespace of the Secret (defaults to the MCPServer's namespace)",
								},
								"clientIdKey": map[string]interface{}{
									api.SchemaKeyType:        string(api.ArgTypeString),
									api.SchemaKeyDescription: "Secret key holding the client ID (default: client-id)",
								},
								"clientSecretKey": map[string]interface{}{
									api.SchemaKeyType:        string(api.ArgTypeString),
									api.SchemaKeyDescription: "Secret key holding the client secret (default: client-secret)",
								},
							},
							api.SchemaKeyRequired: []string{"name"},
						},
						"grantScope": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "Whose token: session (default, the login session that got it) or subject (the person's; later sessions reuse it until core_auth_logout or sign-out)",
							api.SchemaKeyEnum:        []string{api.GrantScopeSession, api.GrantScopeSubject},
						},
					},
					api.SchemaKeyRequired: []string{"issuer"},
				},
				"tokenExchange": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeObject),
					api.SchemaKeyDescription: "RFC 8693 Token Exchange for cross-cluster SSO: trades muster's local token for one from the remote cluster's IdP (wins over forwardToken)",
					api.SchemaKeyProperties: map[string]interface{}{
						"enabled": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeBoolean),
							api.SchemaKeyDescription: "Whether token exchange should be attempted",
						},
						"dexTokenEndpoint": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "URL of the remote cluster's Dex token endpoint (required when enabled)",
						},
						"expectedIssuer": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "Expected issuer URL in the exchanged token's iss claim; derived from dexTokenEndpoint if not set",
						},
						"connectorId": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "ID of the OIDC connector on the remote Dex that trusts the local Dex; required for a person's exchange, a delegated caller's exchange sends none",
						},
						"scopes": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "Scopes to request for the exchanged token (default: openid profile email groups)",
						},
						"clientCredentialsSecretRef": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeObject),
							api.SchemaKeyDescription: "Kubernetes Secret with client credentials for the remote Dex token endpoint",
							api.SchemaKeyProperties: map[string]interface{}{
								"name": map[string]interface{}{
									api.SchemaKeyType:        string(api.ArgTypeString),
									api.SchemaKeyDescription: "Secret name",
								},
								"namespace": map[string]interface{}{
									api.SchemaKeyType:        string(api.ArgTypeString),
									api.SchemaKeyDescription: "Secret namespace (defaults to the MCPServer's namespace)",
								},
								"clientIdKey": map[string]interface{}{
									api.SchemaKeyType:        string(api.ArgTypeString),
									api.SchemaKeyDescription: "Key in the secret containing the client ID (default: client-id)",
								},
								"clientSecretKey": map[string]interface{}{
									api.SchemaKeyType:        string(api.ArgTypeString),
									api.SchemaKeyDescription: "Key in the secret containing the client secret (default: client-secret)",
								},
							},
							api.SchemaKeyRequired: []string{"name"},
						},
					},
				},
			},
		}},
	}
}
