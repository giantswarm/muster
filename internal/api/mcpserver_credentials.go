package api

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// MCPServerHeaderFrom is an HTTP header whose value muster reads from a
// Secret in the server's own namespace when it connects (spec.headersFrom).
// It carries the reference only: the value never enters a definition.
type MCPServerHeaderFrom struct {
	// Name is the HTTP header name.
	Name string `yaml:"name" json:"name"`

	// ValueFrom names the Secret key that holds the value.
	ValueFrom MCPServerValueSource `yaml:"valueFrom" json:"valueFrom"`
}

// MCPServerValueSource names a key of a Secret in the server's own namespace.
// It has no namespace field on purpose.
type MCPServerValueSource struct {
	// Type is the kind of object that holds the value; only "Secret".
	Type string `yaml:"type" json:"type"`

	// Name is the name of the Secret.
	Name string `yaml:"name" json:"name"`

	// Key is the key in the Secret's data.
	Key string `yaml:"key" json:"key"`
}

// ValueSourceTypeSecret is the only MCPServerValueSource.Type.
const ValueSourceTypeSecret = "Secret"

// ErrCredentialInDefinition reports a credential written into an MCPServer
// definition, which everyone who can read the server can read.
var ErrCredentialInDefinition = errors.New("an MCPServer definition holds no credential")

// ErrSecretOutsideNamespace reports a Secret reference of a server outside
// muster's namespace that names another namespace than the server's own.
var ErrSecretOutsideNamespace = errors.New("an MCPServer outside muster's namespace reads Secrets of its own namespace only")

// credentialHeaders are the header names whose value is a credential. The
// CRD's CEL rule on spec.headers names the same set.
var credentialHeaders = []string{"Authorization", "Proxy-Authorization", "Cookie", "X-API-Key"}

// urlUserInfo matches a URL with a user name or password. The CRD's CEL rule
// on spec.url is the same regex: CEL's URL library has no user-info accessor.
var urlUserInfo = regexp.MustCompile(`^[^:/?#]+://[^/?#]*@`)

// ValidateNoInlineCredential reports a credential written into a server's
// definition: env on a remote server (env reaches only a stdio process) in
// every mode, and in Kubernetes mode a value in an Authorization,
// Proxy-Authorization, Cookie or X-API-Key header (in any case) or a user
// name or password in the URL. In Kubernetes mode everyone who can read the
// MCPServer can read the definition, so a credential belongs in a Secret that
// spec.headersFrom refers to. Filesystem mode has no Secret reader, and its
// definition is a file of the person who runs muster, so it keeps its headers.
//
// The CRD states the same rules in CEL, which only runs at admission. This is
// the check the reconciler and core_mcpserver_* run where muster loads a
// server; keep the two in step.
func ValidateNoInlineCredential(serverType, rawURL string, headers, env map[string]string, kubernetesMode bool) error {
	if len(env) > 0 && MCPServerType(serverType).IsRemote() {
		return fmt.Errorf("%w: env is only allowed when type is %q; put a remote server's credential in a Secret and refer to it in headersFrom", ErrCredentialInDefinition, MCPServerTypeStdio)
	}
	if !kubernetesMode {
		return nil
	}
	for name, value := range headers {
		if value == "" {
			continue
		}
		for _, credential := range credentialHeaders {
			if strings.EqualFold(name, credential) {
				return fmt.Errorf("%w: header %s holds a credential; put it in a Secret and refer to it in headersFrom", ErrCredentialInDefinition, credential)
			}
		}
	}
	if urlUserInfo.MatchString(rawURL) {
		return fmt.Errorf("%w: url holds a user name or password; put the credential in a Secret and refer to it in headersFrom", ErrCredentialInDefinition)
	}
	return nil
}

// ValidateHeadersFrom reports a spec.headersFrom that the CRD schema would
// refuse: an entry on a stdio server, or an entry without a header name, a
// Secret name or key, or with a type other than Secret.
func ValidateHeadersFrom(serverType string, headersFrom []MCPServerHeaderFrom) error {
	if len(headersFrom) == 0 {
		return nil
	}
	if !MCPServerType(serverType).IsRemote() {
		return fmt.Errorf("headersFrom is only allowed when type is %q or %q", MCPServerTypeStreamableHTTP, MCPServerTypeSSE)
	}
	for _, h := range headersFrom {
		if h.Name == "" || h.ValueFrom.Name == "" || h.ValueFrom.Key == "" {
			return fmt.Errorf("headersFrom entry %q needs a name, valueFrom.name and valueFrom.key", h.Name)
		}
		if h.ValueFrom.Type != ValueSourceTypeSecret {
			return fmt.Errorf("headersFrom entry %q: valueFrom.type must be %q", h.Name, ValueSourceTypeSecret)
		}
	}
	return nil
}

// ValidateSecretNamespaces reports a Secret reference that would make muster
// read a Secret outside the namespace of a server that is not in muster's own
// namespace: the clientCredentialsSecretRef of auth.tokenExchange or of an
// auth.authorizationServer pin. Such a server could otherwise have a Secret of
// muster's namespace sent to a host it names. A server in muster's namespace,
// or one without a namespace (filesystem mode), keeps today's references.
// spec.headersFrom has no namespace field, so it always reads the server's own.
func ValidateSecretNamespaces(serverNamespace, musterNamespace string, auth *MCPServerAuth) error {
	if serverNamespace == "" || serverNamespace == musterNamespace || auth == nil {
		return nil
	}
	check := func(field string, ref *ClientCredentialsSecretRef) error {
		if ref != nil && ref.Namespace != "" && ref.Namespace != serverNamespace {
			return fmt.Errorf("%w: %s names namespace %q, not the server's namespace %q", ErrSecretOutsideNamespace, field, ref.Namespace, serverNamespace)
		}
		return nil
	}
	if auth.TokenExchange != nil {
		if err := check("auth.tokenExchange.clientCredentialsSecretRef", auth.TokenExchange.ClientCredentialsSecretRef); err != nil {
			return err
		}
	}
	if auth.AuthorizationServer != nil {
		return check("auth.authorizationServer.clientCredentialsSecretRef", auth.AuthorizationServer.ClientCredentialsSecretRef)
	}
	return nil
}
