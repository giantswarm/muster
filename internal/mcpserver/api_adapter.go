package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/callerwrite"
	"github.com/giantswarm/muster/internal/client"
	"github.com/giantswarm/muster/internal/events"
	"github.com/giantswarm/muster/pkg/logging"
)

// convertCRDFamilyToAPI converts a CRD MCPServerFamily to an API MCPServerFamily.
// Returns nil if the input is nil.
func convertCRDFamilyToAPI(src *musterv1alpha1.MCPServerFamily) *api.MCPServerFamily {
	if src == nil {
		return nil
	}
	return &api.MCPServerFamily{
		Name:        src.Name,
		InstanceArg: src.InstanceArg,
	}
}

// convertAPIFamilyToCRD converts an API MCPServerFamily to a CRD MCPServerFamily.
// Returns nil if the input is nil.
func convertAPIFamilyToCRD(src *api.MCPServerFamily) *musterv1alpha1.MCPServerFamily {
	if src == nil {
		return nil
	}
	return &musterv1alpha1.MCPServerFamily{
		Name:        src.Name,
		InstanceArg: src.InstanceArg,
	}
}

// convertCRDSecretRefToAPI converts a CRD ClientCredentialsSecretRef to an API ClientCredentialsSecretRef.
// Returns nil if the input is nil.
func convertCRDSecretRefToAPI(src *musterv1alpha1.ClientCredentialsSecretRef) *api.ClientCredentialsSecretRef {
	if src == nil {
		return nil
	}
	return &api.ClientCredentialsSecretRef{
		Name:            src.Name,
		Namespace:       src.Namespace,
		ClientIDKey:     src.ClientIDKey,
		ClientSecretKey: src.ClientSecretKey,
	}
}

// convertAPISecretRefToCRD converts an API ClientCredentialsSecretRef to a CRD ClientCredentialsSecretRef.
// Returns nil if the input is nil.
func convertAPISecretRefToCRD(src *api.ClientCredentialsSecretRef) *musterv1alpha1.ClientCredentialsSecretRef {
	if src == nil {
		return nil
	}
	return &musterv1alpha1.ClientCredentialsSecretRef{
		Name:            src.Name,
		Namespace:       src.Namespace,
		ClientIDKey:     src.ClientIDKey,
		ClientSecretKey: src.ClientSecretKey,
	}
}

// convertCRDSigV4ToAPI converts a CRD MCPServerSigV4 to an API MCPServerSigV4.
// Returns nil if the input is nil.
func convertCRDSigV4ToAPI(src *musterv1alpha1.MCPServerSigV4) *api.MCPServerSigV4 {
	if src == nil {
		return nil
	}
	return &api.MCPServerSigV4{
		Region:  src.Region,
		Service: src.Service,
		RoleARN: src.RoleARN,
	}
}

// convertAPISigV4ToCRD converts an API MCPServerSigV4 to a CRD MCPServerSigV4.
// Returns nil if the input is nil.
func convertAPISigV4ToCRD(src *api.MCPServerSigV4) *musterv1alpha1.MCPServerSigV4 {
	if src == nil {
		return nil
	}
	return &musterv1alpha1.MCPServerSigV4{
		Region:  src.Region,
		Service: src.Service,
		RoleARN: src.RoleARN,
	}
}

// convertCRDAuthToAPI converts a CRD MCPServerAuth to an API MCPServerAuth.
// Returns nil if the input is nil.
func convertCRDAuthToAPI(src *musterv1alpha1.MCPServerAuth) *api.MCPServerAuth {
	if src == nil {
		return nil
	}
	auth := &api.MCPServerAuth{
		Type:              src.Type,
		ForwardToken:      src.ForwardToken,
		RequiredAudiences: src.RequiredAudiences,
		SigV4:             convertCRDSigV4ToAPI(src.SigV4),
	}
	if src.TokenExchange != nil {
		auth.TokenExchange = &api.TokenExchangeConfig{
			Enabled:                    src.TokenExchange.Enabled,
			DexTokenEndpoint:           src.TokenExchange.DexTokenEndpoint,
			ExpectedIssuer:             src.TokenExchange.ExpectedIssuer,
			ConnectorID:                src.TokenExchange.ConnectorID,
			Scopes:                     src.TokenExchange.Scopes,
			ClientCredentialsSecretRef: convertCRDSecretRefToAPI(src.TokenExchange.ClientCredentialsSecretRef),
		}
	}
	if src.AuthorizationServer != nil {
		auth.AuthorizationServer = &api.MCPServerAuthAuthorizationServer{
			Issuer: src.AuthorizationServer.Issuer.Normalize(),
			Scopes: src.AuthorizationServer.Scopes,
		}
	}
	return auth
}

// convertAPIAuthToCRD converts an API MCPServerAuth to a CRD MCPServerAuth.
// Returns nil if the input is nil.
//
// Create and update share this so a new auth field cannot reach the CR through
// one path and be dropped by the other.
func convertAPIAuthToCRD(src *api.MCPServerAuth) *musterv1alpha1.MCPServerAuth {
	if src == nil {
		return nil
	}
	auth := &musterv1alpha1.MCPServerAuth{
		Type:              src.Type,
		ForwardToken:      src.ForwardToken,
		RequiredAudiences: src.RequiredAudiences,
		SigV4:             convertAPISigV4ToCRD(src.SigV4),
	}
	if src.TokenExchange != nil {
		auth.TokenExchange = &musterv1alpha1.TokenExchangeConfig{
			Enabled:                    src.TokenExchange.Enabled,
			DexTokenEndpoint:           src.TokenExchange.DexTokenEndpoint,
			ExpectedIssuer:             src.TokenExchange.ExpectedIssuer,
			ConnectorID:                src.TokenExchange.ConnectorID,
			Scopes:                     src.TokenExchange.Scopes,
			ClientCredentialsSecretRef: convertAPISecretRefToCRD(src.TokenExchange.ClientCredentialsSecretRef),
		}
	}
	if src.AuthorizationServer != nil {
		// Normalize is the canonical form, so use it rather than restating what
		// it does — convertCRDAuthToAPI already calls it in the other direction.
		issuer := musterv1alpha1.IssuerURL(src.AuthorizationServer.Issuer)
		auth.AuthorizationServer = &musterv1alpha1.MCPServerAuthAuthorizationServer{
			Issuer: musterv1alpha1.IssuerURL(issuer.Normalize()),
			Scopes: src.AuthorizationServer.Scopes,
		}
	}
	return auth
}

// Adapter provides MCP server management functionality using the unified client
type Adapter struct {
	client    client.MusterClient
	namespace string

	// gate switches session-initiated spec mutations (create/update/delete)
	// from the shared SA-backed client to a per-call client bearing the
	// caller's own dex id_token (issue #1056). Always enabled in Kubernetes
	// mode; filesystem mode keeps the local client. Reads, validation, and
	// muster's own controller writes are unaffected.
	gate callerwrite.Gate
}

// mcpServerWriter is the write surface the mutation handlers go through. The
// SA-backed MusterClient and the per-call caller-bearer client both satisfy it.
type mcpServerWriter interface {
	CreateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error
	UpdateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error
	DeleteMCPServer(ctx context.Context, name, namespace string) error
}

// NewAdapterWithClient creates a new adapter with a specific client (for testing)
func NewAdapterWithClient(musterClient client.MusterClient, namespace string) *Adapter {
	if namespace == "" {
		namespace = "default"
	}
	return &Adapter{
		client:    musterClient,
		namespace: namespace,
		gate:      callerwrite.NewGate("MCP server changes"),
	}
}

// EnableWritesAsCaller switches session-initiated mutations to
// caller-identity writes; the app layer calls it whenever muster runs in
// Kubernetes mode. factory may be nil when muster has no Kubernetes API
// access; mutations then fail with an explicit configuration error instead of
// silently falling back to the SA path. An empty kubernetesAudience selects
// the default.
func (a *Adapter) EnableWritesAsCaller(factory callerwrite.ClientFactory, kubernetesAudience string) {
	a.gate.Enable(factory, kubernetesAudience)
}

// mutationWriter resolves the write client a mutation must use. In
// filesystem mode it is the shared local client. In Kubernetes mode the
// session's dex id_token becomes the bearer of a per-call client, so the
// apiserver authenticates the real user and k8s RBAC decides. A nil writer is
// returned together with a ready-to-return tool error when the session cannot
// produce a usable bearer.
func (a *Adapter) mutationWriter(ctx context.Context) (mcpServerWriter, *api.CallToolResult) {
	if !a.gate.Enabled() {
		return a.client, nil
	}
	callerClient, errMsg := a.gate.Resolve(ctx)
	if errMsg != "" {
		result, _ := simpleError(errMsg)
		return nil, result
	}
	return &callerWriter{client: callerClient, namespace: a.namespace}, nil
}

// describeWriteAuthError maps apiserver authn/authz failures on
// caller-identity writes to actionable tool errors: 403 names the verb,
// resource, and namespace; 401 asks for a re-login. Returns "" for every
// other error so callers fall through to their existing handling.
func (a *Adapter) describeWriteAuthError(err error, verb, name string) string {
	return callerwrite.DescribeWriteAuthError(err, verb,
		"MCPServer", "mcpservers.muster.giantswarm.io", name, a.namespace, "mcpserver-editor")
}

// Register registers the adapter with the API
func (a *Adapter) Register() {
	api.RegisterMCPServerManager(a)
}

// Close performs cleanup for the adapter
func (a *Adapter) Close() error {
	if a.client != nil {
		return a.client.Close()
	}
	return nil
}

// ListMCPServers returns all MCP server definitions
func (a *Adapter) ListMCPServers(ctx context.Context) ([]api.MCPServerInfo, error) {
	servers, err := a.client.ListMCPServers(ctx, a.namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to list MCPServers: %w", err)
	}

	result := make([]api.MCPServerInfo, len(servers))
	for i, server := range servers {
		result[i] = convertCRDToInfo(&server)
		result[i].ProtocolVersion = serviceProtocolVersion(server.Name)
	}

	return result, nil
}

// serviceProtocolVersion returns the MCP revision the named server answered
// with during its handshake, or "" when no service is running for it.
//
// It reads the live service rather than the CRD status so that the value
// matches what core_service_status reports for the same server; the status
// subresource syncs on an interval and would lag behind a reconnect that
// negotiates a different revision.
func serviceProtocolVersion(name string) string {
	registry := api.GetServiceRegistry()
	if registry == nil {
		return ""
	}

	service, exists := registry.Get(name)
	if !exists {
		return ""
	}

	version, _ := service.GetServiceData()[api.ServiceDataProtocolVersion].(string)
	return version
}

// GetMCPServer returns information about a specific MCP server
func (a *Adapter) GetMCPServer(name string) (*api.MCPServerInfo, error) {
	ctx := context.Background()

	server, err := a.client.GetMCPServer(ctx, name, a.namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, api.NewMCPServerNotFoundError(name)
		}
		return nil, fmt.Errorf("failed to get MCPServer %s: %w", name, err)
	}

	info := convertCRDToInfo(server)
	info.ProtocolVersion = serviceProtocolVersion(name)
	return &info, nil
}

// convertCRDToInfo converts a MCPServer CRD to MCPServerInfo
func convertCRDToInfo(server *musterv1alpha1.MCPServer) api.MCPServerInfo {
	info := api.MCPServerInfo{
		Name:                server.Name,
		Type:                server.Spec.Type,
		Description:         server.Spec.Description,
		ToolPrefix:          server.Spec.ToolPrefix,
		Family:              convertCRDFamilyToAPI(server.Spec.Family),
		AutoStart:           server.Spec.AutoStart,
		Suspended:           server.Spec.Suspended,
		Command:             server.Spec.Command,
		Args:                server.Spec.Args,
		URL:                 server.Spec.URL,
		Env:                 server.Spec.Env,
		Headers:             server.Spec.Headers,
		Meta:                server.Spec.Meta,
		Timeout:             server.Spec.Timeout,
		Error:               server.Status.LastError,
		State:               string(server.Status.State),
		ConsecutiveFailures: server.Status.ConsecutiveFailures,
	}

	// Convert time fields from metav1.Time to time.Time. The lifecycle
	// timestamps are normalized to UTC so their JSON rendering is stable
	// regardless of the process's local timezone.
	if server.Spec.RestartRequestedAt != nil {
		t := server.Spec.RestartRequestedAt.UTC()
		info.RestartRequestedAt = &t
	}
	if server.Status.LastRestartedAt != nil {
		t := server.Status.LastRestartedAt.UTC()
		info.LastRestartedAt = &t
	}
	if server.Status.LastAttempt != nil {
		t := server.Status.LastAttempt.Time
		info.LastAttempt = &t
	}
	if server.Status.NextRetryAfter != nil {
		t := server.Status.NextRetryAfter.Time
		info.NextRetryAfter = &t
	}

	info.Auth = convertCRDAuthToAPI(server.Spec.Auth)

	// Generate user-friendly status message based on state and error
	info.StatusMessage = generateStatusMessage(info.State, info.Error, server.Name, info.Auth)

	info.RegisteredBy = server.Annotations[api.RegisteredByAnnotation]
	info.RegisteredByEmail = server.Annotations[api.RegisteredByEmailAnnotation]

	return info
}

// subjectFromContext resolves the authenticated caller's subject, mirroring the
// aggregator's resolution order: the injector middleware's context key first,
// then the mcp-oauth UserInfo (which survives middleware early-exit paths).
// Returns "" for unauthenticated transports (stdio, plain HTTP).
func subjectFromContext(ctx context.Context) string {
	if sub := api.GetSubjectFromContext(ctx); sub != "" {
		return sub
	}
	if userInfo, ok := oauthhandler.UserInfoFromContext(ctx); ok && userInfo != nil {
		return userInfo.ID
	}
	return ""
}

// emailFromContext resolves the authenticated caller's email claim, if the
// token carried one. Display metadata only — the subject is the stable
// identifier. Returns "" for identities without an email (e.g. Kubernetes
// ServiceAccounts) and unauthenticated transports.
func emailFromContext(ctx context.Context) string {
	if userInfo, ok := oauthhandler.UserInfoFromContext(ctx); ok && userInfo != nil {
		return userInfo.Email
	}
	return ""
}

// generateStatusMessage creates a user-friendly, actionable status message
// based on the server's state and error information.
//
// State is infrastructure-only:
//   - Running/Connected: Infrastructure reachable (no message needed)
//   - Starting/Connecting: In progress
//   - Stopped/Disconnected: Not running (no message needed)
//   - Failed: Infrastructure unavailable (include error context)
func generateStatusMessage(state, errorMsg, serverName string, auth *api.MCPServerAuth) string {
	switch state {
	case "Running", "Connected":
		return ""
	case "Starting", "Connecting":
		return "Starting..."
	case "Stopped", "Disconnected":
		return ""
	case "Failed":
		return generateFailedMessage(errorMsg, serverName, auth)
	default:
		return ""
	}
}

// generateFailedMessage creates a user-friendly message for failed servers.
//
// auth decides what a 401 means. For an auth type with no interactive login the
// advice cannot be "run muster auth login" — there is no user credential to
// obtain, and the real cause is the signing configuration or the identity muster
// signs as.
func generateFailedMessage(errorMsg, serverName string, auth *api.MCPServerAuth) string {
	if errorMsg == "" {
		return "Server failed to start"
	}

	lowerErr := strings.ToLower(errorMsg)

	// Check for specific error patterns and provide actionable messages
	switch {
	case strings.Contains(lowerErr, "certificate") || strings.Contains(lowerErr, "x509"):
		return "Certificate error - verify TLS configuration"
	case strings.Contains(lowerErr, "tls handshake"):
		return "TLS error - check server certificate and TLS configuration"
	case strings.Contains(lowerErr, "command not found") || strings.Contains(lowerErr, "executable file not found"):
		return "Command not found - check the executable path"
	case strings.Contains(lowerErr, "permission denied"):
		return "Permission denied - check file permissions"
	case strings.Contains(lowerErr, "401") || strings.Contains(lowerErr, "unauthorized"):
		if !auth.CanAuthenticateInteractively() {
			return "Endpoint rejected muster's credential - check the signing region, the assumed role and its policy"
		}
		return fmt.Sprintf("Authentication required - run: muster auth login --server %s", serverName)
	case strings.Contains(lowerErr, "403") || strings.Contains(lowerErr, "forbidden"):
		return "Access forbidden - check server permissions and credentials"
	case strings.Contains(lowerErr, "connection reset") || strings.Contains(lowerErr, "econnreset"):
		return "Connection was reset by server - check server logs"
	case strings.Contains(lowerErr, "protocol") || strings.Contains(lowerErr, "unsupported"):
		return "Protocol error - check server type configuration"
	case strings.Contains(lowerErr, "json") || strings.Contains(lowerErr, "parse"):
		return "Invalid response from server - check server compatibility"
	default:
		return "Server failed to start"
	}
}

// convertRequestToCRD converts a request to a MCPServer CRD using the flat structure
func (a *Adapter) convertRequestToCRD(req *api.MCPServerCreateRequest) *musterv1alpha1.MCPServer {
	crd := &musterv1alpha1.MCPServer{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "muster.giantswarm.io/v1alpha1",
			Kind:       "MCPServer",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: a.namespace,
		},
		Spec: musterv1alpha1.MCPServerSpec{
			Type:        req.Type,
			ToolPrefix:  req.ToolPrefix,
			Family:      convertAPIFamilyToCRD(req.Family),
			Description: req.Description,
			AutoStart:   req.AutoStart,
			Command:     req.Command,
			Args:        req.Args,
			URL:         req.URL,
			Env:         req.Env,
			Headers:     req.Headers,
			Meta:        req.Meta,
			Timeout:     req.Timeout,
			Auth:        convertAPIAuthToCRD(req.Auth),
		},
	}

	return crd
}

// ToolProvider implementation

// mcpServerArgs returns the common argument metadata for MCP server tools.
// typeRequired controls whether the "type" field is required (true for create/validate, false for update).
func mcpServerArgs(typeRequired bool) []api.ArgMetadata {
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
		{Name: "description", Type: api.ArgTypeString, Required: false, Description: "MCP server description"},
		{Name: "autoStart", Type: api.ArgTypeBoolean, Required: false, Description: "Whether server should auto-start"},
		{Name: "command", Type: api.ArgTypeString, Required: false, Description: "Command executable path (required for stdio)"},
		{Name: "args", Type: api.ArgTypeArray, Required: false, Description: "Command arguments (stdio only)", Schema: map[string]interface{}{
			api.SchemaKeyType:        string(api.ArgTypeArray),
			api.SchemaKeyItems:       map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString)},
			api.SchemaKeyDescription: "Command line arguments for stdio servers",
		}},
		{Name: "url", Type: api.ArgTypeString, Required: false, Description: "Server endpoint URL (required for streamable-http and sse)"},
		{Name: "env", Type: api.ArgTypeObject, Required: false, Description: "Environment variables", Schema: map[string]interface{}{
			api.SchemaKeyType:                 string(api.ArgTypeObject),
			api.SchemaKeyAdditionalProperties: map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString)},
			api.SchemaKeyDescription:          "Environment variables for the server",
		}},
		{Name: "headers", Type: api.ArgTypeObject, Required: false, Description: "HTTP headers (streamable-http and sse only)", Schema: map[string]interface{}{
			api.SchemaKeyType:                 string(api.ArgTypeObject),
			api.SchemaKeyAdditionalProperties: map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString)},
			api.SchemaKeyDescription:          "HTTP headers for remote servers",
		}},
		{Name: "meta", Type: api.ArgTypeObject, Required: false, Description: "Entries merged into the params._meta object of every outbound JSON-RPC request that carries params (applied by the sigv4 transport only)", Schema: map[string]interface{}{
			api.SchemaKeyType:                 string(api.ArgTypeObject),
			api.SchemaKeyAdditionalProperties: map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString)},
			api.SchemaKeyDescription:          "Request metadata passthrough, for backends that read call-scoped configuration from params._meta instead of from tool arguments. A value already present in a request wins. Injection happens in the sigv4 signing transport, so it takes effect only when auth.type is sigv4.",
		}},
		{Name: "timeout", Type: api.ArgTypeInteger, Required: false, Description: "Connection timeout in seconds"},
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
					api.SchemaKeyDescription: "AWS Signature Version 4 request signing, using muster's own machine identity. Required when type is sigv4, and rejected otherwise. Only valid with type streamable-http.",
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
				"forwardToken": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeBoolean),
					api.SchemaKeyDescription: "Enable SSO token forwarding (oauth only)",
				},
				"requiredAudiences": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeArray),
					api.SchemaKeyItems:       map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString)},
					api.SchemaKeyDescription: "Additional audiences to request from IdP for token forwarding (e.g., dex-k8s-authenticator for Kubernetes OIDC)",
				},
				"authorizationServer": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeObject),
					api.SchemaKeyDescription: "Pins the OAuth authorization server when the MCP server does not publish RFC 9728 Protected Resource Metadata; skips PRM probing",
					api.SchemaKeyProperties: map[string]interface{}{
						"issuer": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "OAuth 2.0 / OIDC issuer URL (HTTPS, no trailing slash)",
						},
						"scopes": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "OAuth scope parameter value (space-separated scope tokens)",
						},
					},
					api.SchemaKeyRequired: []string{"issuer"},
				},
				"tokenExchange": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeObject),
					api.SchemaKeyDescription: "RFC 8693 Token Exchange for cross-cluster SSO; exchanges muster's local token for one issued by the remote cluster's IdP (takes precedence over forwardToken)",
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
							api.SchemaKeyDescription: "ID of the OIDC connector on the remote Dex that trusts the local Dex (required when enabled)",
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

// GetTools returns all tools this provider offers
func (a *Adapter) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		{
			Name:        "mcpserver_list",
			Description: "List all MCP server definitions with their status. By default, unreachable servers are hidden.",
			Args: []api.ArgMetadata{
				{Name: "showAll", Type: api.ArgTypeBoolean, Required: false, Description: "Show all servers including unreachable ones (default: false)"},
				{Name: "verbose", Type: api.ArgTypeBoolean, Required: false, Description: "Show detailed error information for failed/unreachable servers (default: false)"},
			},
		},
		{
			Name:        "mcpserver_get",
			Description: "Get detailed information about a specific MCP server definition",
			Args: []api.ArgMetadata{
				{Name: "name", Type: api.ArgTypeString, Required: true, Description: "Name of the MCP server to retrieve"},
			},
		},
		{
			Name:        "mcpserver_validate",
			Description: "Validate an mcpserver definition",
			Args:        mcpServerArgs(true), // type is required for validation
		},
		{
			Name:        "mcpserver_detect",
			Description: "Probe a remote MCP server URL to detect its transport (streamable-http or sse). Detection never fails on unreachable servers: the result reports transport \"unknown\" instead, so callers can fall back to manual selection.",
			Args: []api.ArgMetadata{
				{Name: "url", Type: api.ArgTypeString, Required: true, Description: "Server endpoint URL to probe"},
				{Name: "headers", Type: api.ArgTypeObject, Required: false, Description: "HTTP headers to send with the probe requests", Schema: map[string]interface{}{
					api.SchemaKeyType:                 string(api.ArgTypeObject),
					api.SchemaKeyAdditionalProperties: map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString)},
					api.SchemaKeyDescription:          "HTTP headers for the probe requests",
				}},
				{Name: "timeout", Type: api.ArgTypeInteger, Required: false, Description: "Overall detection timeout in seconds (default 10)"},
			},
		},
		{
			Name:        "mcpserver_create",
			Description: "Create a new MCP server definition",
			Args:        mcpServerArgs(true), // type is required for creation
		},
		{
			Name:        "mcpserver_update",
			Description: "Update an existing MCP server definition",
			// type is optional for update; suspended/restartRequestedAt are the
			// CR-driven lifecycle fields (issue #1055) and only settable here.
			Args: append(mcpServerArgs(false),
				api.ArgMetadata{Name: "suspended", Type: api.ArgTypeBoolean, Required: false, Description: "Desired lifecycle state: true stops the server's service and keeps it stopped; false resumes it; omitted keeps the current value"},
				api.ArgMetadata{Name: "restartRequestedAt", Type: api.ArgTypeString, Required: false, Description: "RFC 3339 timestamp requesting a one-shot restart; processed once by the reconciler"},
			),
		},
		{
			Name:        "mcpserver_delete",
			Description: "Delete an MCP server definition",
			Args: []api.ArgMetadata{
				{Name: "name", Type: api.ArgTypeString, Required: true, Description: "Name of the MCP server to delete"},
			},
		},
	}
}

// ExecuteTool executes a tool by name
func (a *Adapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch toolName {
	case "mcpserver_list":
		return a.handleMCPServerList(ctx, args)
	case "mcpserver_get":
		return a.handleMCPServerGet(args)
	case "mcpserver_validate":
		return a.handleMCPServerValidate(args)
	case "mcpserver_detect":
		return a.handleMCPServerDetect(ctx, args)
	case "mcpserver_create":
		return a.handleMCPServerCreate(ctx, args)
	case "mcpserver_update":
		return a.handleMCPServerUpdate(ctx, args)
	case "mcpserver_delete":
		return a.handleMCPServerDelete(ctx, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

// Tool handlers

func (a *Adapter) handleMCPServerList(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	allServers, err := a.ListMCPServers(ctx)
	if err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to list MCP servers"), nil
	}

	// Check showAll parameter (default: false)
	showAll := false
	if val, ok := args["showAll"].(bool); ok {
		showAll = val
	}

	// Check verbose parameter (default: false)
	verbose := false
	if val, ok := args["verbose"].(bool); ok {
		verbose = val
	}

	// Filter out failed servers unless showAll is true
	// Per issue #292, Failed phase indicates infrastructure unavailable
	var filteredServers []api.MCPServerInfo
	failedCount := 0
	for _, server := range allServers {
		// Adjust server for display (hide raw errors in non-verbose mode)
		server = adjustServerForDisplay(server, verbose)

		if server.State == "Failed" {
			failedCount++
			if showAll {
				filteredServers = append(filteredServers, server)
			}
		} else {
			filteredServers = append(filteredServers, server)
		}
	}

	result := map[string]interface{}{
		"mcpServers": filteredServers,
		"total":      len(filteredServers),
		"mode":       getClientMode(a.client),
	}

	// Add failed count if any servers were hidden
	if failedCount > 0 && !showAll {
		result["hiddenFailed"] = failedCount
		result["hint"] = fmt.Sprintf("(%d failed servers hidden, use --all to show, --verbose for details)", failedCount)
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

// adjustServerForDisplay adjusts server fields for user-friendly display.
// In non-verbose mode, hide raw error messages (statusMessage provides user-friendly version).
func adjustServerForDisplay(server api.MCPServerInfo, verbose bool) api.MCPServerInfo {
	// If not verbose, don't include the raw error message.
	// The statusMessage field provides a user-friendly version.
	if !verbose {
		server.Error = ""
	}

	return server
}

func (a *Adapter) handleMCPServerGet(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name argument is required"},
			IsError: true,
		}, nil
	}

	mcpServer, err := a.GetMCPServer(name)
	if err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to get MCP server"), nil
	}

	return &api.CallToolResult{
		Content: []interface{}{mcpServer},
		IsError: false,
	}, nil
}

// handleMCPServerValidate validates an mcpserver definition
func (a *Adapter) handleMCPServerValidate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.MCPServerValidateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Create MCPServer CRD for validation
	server := a.convertRequestToCRD(&api.MCPServerCreateRequest{
		Name:        req.Name,
		Type:        req.Type,
		ToolPrefix:  req.ToolPrefix,
		Family:      req.Family,
		Description: req.Description,
		AutoStart:   req.AutoStart,
		Command:     req.Command,
		Args:        req.Args,
		URL:         req.URL,
		Env:         req.Env,
		Headers:     req.Headers,
		Meta:        req.Meta,
		Timeout:     req.Timeout,
		Auth:        req.Auth,
	})

	// Basic validation (more comprehensive validation would be done by the CRD schema)
	if err := a.validateMCPServer(server); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Validation failed: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Validation successful for mcpserver %s", req.Name)},
		IsError: false,
	}, nil
}

// handleMCPServerDetect probes a remote URL to detect its MCP transport.
// The result is returned both as JSON text content and as structuredContent;
// only a missing/invalid url argument is a tool error — an unreachable or
// unclassifiable server yields a success result with transport "unknown".
func (a *Adapter) handleMCPServerDetect(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.MCPServerDetectRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	if req.URL == "" {
		return simpleError("url argument is required")
	}

	result := DetectTransport(ctx, req.URL, req.Headers, time.Duration(req.Timeout)*time.Second)

	return &api.CallToolResult{
		Content:           []interface{}{result},
		StructuredContent: result,
		IsError:           false,
	}, nil
}

func (a *Adapter) handleMCPServerCreate(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.MCPServerCreateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	writer, errResult := a.mutationWriter(ctx)
	if errResult != nil {
		return errResult, nil
	}

	// Convert request to CRD once for reuse
	serverCRD := a.convertRequestToCRD(&req)

	// Stamp the authenticated subject so registration is attributable even for
	// clients that don't stamp it themselves (issue #1021). Update preserves it
	// via its get-modify-update flow. The email claim, when present, is stamped
	// alongside as display metadata (issue #1048) — the subject stays the
	// stable identifier.
	if subject := subjectFromContext(ctx); subject != "" {
		serverCRD.Annotations = map[string]string{api.RegisteredByAnnotation: subject}
		if email := emailFromContext(ctx); email != "" {
			serverCRD.Annotations[api.RegisteredByEmailAnnotation] = email
		}
	}

	// Validate the definition
	if err := a.validateMCPServer(serverCRD); err != nil {
		return simpleError(fmt.Sprintf("Invalid MCP server definition: %v", err))
	}

	// Create the new MCP server with the resolved write identity
	if err := writer.CreateMCPServer(ctx, serverCRD); err != nil {
		if errors.IsAlreadyExists(err) {
			return simpleError(fmt.Sprintf("MCP server '%s' already exists", req.Name))
		}
		if msg := a.describeWriteAuthError(err, "create", req.Name); msg != "" {
			return simpleError(msg)
		}
		// Generate failure event
		a.generateCRDEvent(req.Name, events.ReasonMCPServerFailed, events.EventData{
			Error:     err.Error(),
			Operation: "create",
		})
		return simpleError(fmt.Sprintf("Failed to create MCP server: %v", err))
	}

	// Generate success event for CRD creation
	a.generateCRDEvent(req.Name, events.ReasonMCPServerCreated, events.EventData{
		Operation: "create",
	})

	return simpleOK(fmt.Sprintf("MCP server '%s' created successfully", req.Name))
}

func (a *Adapter) handleMCPServerUpdate(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.MCPServerUpdateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	writer, errResult := a.mutationWriter(ctx)
	if errResult != nil {
		return errResult, nil
	}

	// Get existing server first. The read stays on the SA client — only the
	// spec mutation itself switches to the caller's identity.
	existing, err := a.client.GetMCPServer(ctx, req.Name, a.namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return api.HandleErrorWithPrefix(api.NewMCPServerNotFoundError(req.Name), "Failed to update MCP server"), nil
		}
		return simpleError(fmt.Sprintf("Failed to get existing MCP server: %v", err))
	}

	// Update common fields from request
	if req.Type != "" {
		existing.Spec.Type = req.Type
	}
	if req.ToolPrefix != "" {
		existing.Spec.ToolPrefix = req.ToolPrefix
	}
	if req.Family != nil {
		existing.Spec.Family = convertAPIFamilyToCRD(req.Family)
	}
	if req.Description != "" {
		existing.Spec.Description = req.Description
	}
	existing.Spec.AutoStart = req.AutoStart
	// Tri-state: only an explicit suspended value changes the lifecycle state,
	// so unrelated updates don't silently resume a stopped server (issue #1057).
	if req.Suspended != nil {
		existing.Spec.Suspended = *req.Suspended
	}
	if req.RestartRequestedAt != nil {
		existing.Spec.RestartRequestedAt = &metav1.Time{Time: *req.RestartRequestedAt}
	}
	if req.Command != "" {
		existing.Spec.Command = req.Command
	}
	if req.Args != nil {
		existing.Spec.Args = req.Args
	}
	if req.URL != "" {
		existing.Spec.URL = req.URL
	}
	if req.Env != nil {
		existing.Spec.Env = req.Env
	}
	if req.Headers != nil {
		existing.Spec.Headers = req.Headers
	}
	if req.Meta != nil {
		existing.Spec.Meta = req.Meta
	}
	if req.Timeout > 0 {
		existing.Spec.Timeout = req.Timeout
	}
	// Update auth configuration if provided
	if req.Auth != nil {
		existing.Spec.Auth = convertAPIAuthToCRD(req.Auth)
	}

	// Validate the updated definition (reuse existing CRD object)
	if err := a.validateMCPServer(existing); err != nil {
		return simpleError(fmt.Sprintf("Invalid MCP server definition: %v", err))
	}

	// Update the MCP server with the resolved write identity
	if err := writer.UpdateMCPServer(ctx, existing); err != nil {
		if msg := a.describeWriteAuthError(err, "update", req.Name); msg != "" {
			return simpleError(msg)
		}
		// Generate failure event
		a.generateCRDEvent(req.Name, events.ReasonMCPServerFailed, events.EventData{
			Error:     err.Error(),
			Operation: "update",
		})
		return api.HandleErrorWithPrefix(err, "Failed to update MCP server"), nil
	}

	// Generate success event for CRD update
	a.generateCRDEvent(req.Name, events.ReasonMCPServerUpdated, events.EventData{
		Operation: "update",
	})

	return simpleOK(fmt.Sprintf("MCP server '%s' updated successfully", req.Name))
}

func (a *Adapter) handleMCPServerDelete(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return simpleError("name argument is required")
	}

	writer, errResult := a.mutationWriter(ctx)
	if errResult != nil {
		return errResult, nil
	}

	// Delete the MCP server with the resolved write identity
	if err := writer.DeleteMCPServer(ctx, name, a.namespace); err != nil {
		if errors.IsNotFound(err) {
			return api.HandleErrorWithPrefix(api.NewMCPServerNotFoundError(name), "Failed to delete MCP server"), nil
		}
		if msg := a.describeWriteAuthError(err, "delete", name); msg != "" {
			return simpleError(msg)
		}
		// Generate failure event
		a.generateCRDEvent(name, events.ReasonMCPServerFailed, events.EventData{
			Error:     err.Error(),
			Operation: "delete",
		})
		return api.HandleErrorWithPrefix(err, "Failed to delete MCP server"), nil
	}

	// Generate success event for CRD deletion
	a.generateCRDEvent(name, events.ReasonMCPServerDeleted, events.EventData{
		Operation: "delete",
	})

	return simpleOK(fmt.Sprintf("MCP server '%s' deleted successfully", name))
}

// validateMCPServer performs basic validation on an MCP server
func (a *Adapter) validateMCPServer(server *musterv1alpha1.MCPServer) error {
	if server.Name == "" {
		return fmt.Errorf("name is required")
	}

	if server.Spec.Type == "" {
		return fmt.Errorf("type is required")
	}

	switch server.Spec.Type {
	case string(api.MCPServerTypeStdio):
		// Rejected at admission time in Kubernetes mode, so mcpserver_validate
		// reports it before a caller writes the CR and create/update fail the
		// tool call instead of the connect attempt (issue #1067).
		if err := api.ValidateStdioAllowed(server.Spec.Type, a.client.IsKubernetesMode()); err != nil {
			return err
		}
		if server.Spec.Command == "" {
			return fmt.Errorf("command is required for stdio type")
		}
		// Auth is not supported for stdio servers
		if server.Spec.Auth != nil && server.Spec.Auth.Type != "" && server.Spec.Auth.Type != "none" {
			return fmt.Errorf("auth configuration is only supported for remote server types (streamable-http or sse)")
		}
	case string(api.MCPServerTypeStreamableHTTP), string(api.MCPServerTypeSSE):
		if server.Spec.URL == "" {
			return fmt.Errorf("url is required for streamable-http and sse types")
		}
		// Note: timeout defaults to 30 seconds via CRD kubebuilder:default
	default:
		return fmt.Errorf("unsupported MCP server type: %s (supported: %s, %s, %s)",
			server.Spec.Type, api.MCPServerTypeStdio, api.MCPServerTypeStreamableHTTP, api.MCPServerTypeSSE)
	}

	// Reported before the CR is written, so mcpserver_validate and the failing
	// create/update agree with what the connect attempt would do. The CRD says
	// the same in CEL, which does not run in filesystem mode.
	if err := api.ValidateMetaAllowed(server.Spec.Type, server.Spec.Meta); err != nil {
		return err
	}
	return api.ValidateSigV4(server.Spec.Type, convertCRDAuthToAPI(server.Spec.Auth))
}

// helper to create simple error CallToolResult
func simpleError(msg string) (*api.CallToolResult, error) {
	return &api.CallToolResult{Content: []interface{}{msg}, IsError: true}, nil
}

func simpleOK(msg string) (*api.CallToolResult, error) {
	return &api.CallToolResult{Content: []interface{}{msg}, IsError: false}, nil
}

// getClientMode returns a string indicating whether we're in Kubernetes or filesystem mode
func getClientMode(client client.MusterClient) string {
	if client.IsKubernetesMode() {
		return "kubernetes"
	}
	return "filesystem"
}

// generateCRDEvent creates a Kubernetes event for MCPServer CRD operations
func (a *Adapter) generateCRDEvent(name string, reason events.EventReason, data events.EventData) {
	eventManager := api.GetEventManager()
	if eventManager == nil {
		// Event manager not available, skip event generation
		return
	}

	// Create an object reference for the MCPServer CRD
	objectRef := api.ObjectReference{
		Kind:      "MCPServer",
		Name:      name,
		Namespace: a.namespace,
	}

	// Populate event data
	data.Name = name
	if data.Namespace == "" {
		data.Namespace = a.namespace
	}

	err := eventManager.CreateEventWithData(context.Background(), objectRef, string(reason), data.ToAPI())
	if err != nil {
		// Log error but don't fail the operation
		logging.Debug("MCPServer", "Failed to generate event %s for MCPServer %s: %v", string(reason), name, err)
	} else {
		logging.Debug("MCPServer", "Generated event %s for MCPServer %s", string(reason), name)
	}
}
