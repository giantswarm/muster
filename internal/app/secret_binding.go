package app

import (
	"k8s.io/apimachinery/pkg/types"

	mcpserverPkg "github.com/giantswarm/muster/v5/internal/mcpserver"
)

// credentialsOptions returns the secret binding of the credentials adapter:
// muster's own Secrets (secretBinding.protectedSecrets, in its namespace),
// which no MCPServer may read, and, in Kubernetes mode, the admission policy
// that must be in force for muster to read any Secret for an MCPServer
// (secretBinding.admissionPolicyBinding). Secrets muster's configuration
// names (provider Apps, broker targets and clients) are not protected: an
// MCPServer may share them (a broker target's token-exchange Secret, a
// provider App's client).
func credentialsOptions(cfg *Config, namespace string, kubernetesMode bool) []mcpserverPkg.CredentialsOption {
	opts := []mcpserverPkg.CredentialsOption{
		mcpserverPkg.WithProtectedSecrets(protectedSecrets(cfg, namespace)...),
	}
	if kubernetesMode {
		opts = append(opts, mcpserverPkg.WithSecretReferencePolicy(cfg.MusterConfig.SecretBinding.AdmissionPolicyBinding))
	}
	return opts
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
