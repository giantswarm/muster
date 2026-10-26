package app

import (
	"k8s.io/apimachinery/pkg/types"

	mcpserverPkg "github.com/giantswarm/muster/v5/internal/mcpserver"
)

// credentialsOptions returns the secret binding of the credentials adapter:
// muster's own Secrets (secretBinding.protectedSecrets, in its namespace),
// which no MCPServer may read. Secrets muster's configuration names
// (provider Apps, broker targets and clients) are not protected: an
// MCPServer may share them (a broker target's token-exchange Secret, a
// provider App's client).
func credentialsOptions(cfg *Config, namespace string) []mcpserverPkg.CredentialsOption {
	return []mcpserverPkg.CredentialsOption{
		mcpserverPkg.WithProtectedSecrets(protectedSecrets(cfg, namespace)...),
	}
}

// protectedSecrets lists muster's own Secrets, in muster's namespace.
func protectedSecrets(cfg *Config, namespace string) []types.NamespacedName {
	var out []types.NamespacedName
	for _, name := range cfg.MusterConfig.SecretBinding.ProtectedSecrets {
		if name != "" {
			out = append(out, types.NamespacedName{Namespace: namespace, Name: name})
		}
	}
	return out
}
