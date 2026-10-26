package app

import (
	"k8s.io/apimachinery/pkg/types"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	mcpserverPkg "github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// credentialsOptions returns the secret binding of the credentials adapter:
// the mode, and muster's own Secrets (secretBinding.protectedSecrets, in its
// namespace), which no MCPServer may read. Secrets muster's configuration
// names (provider Apps, broker targets and clients) are not protected: an
// MCPServer may share them (a broker target's token-exchange Secret, a
// provider App's client) once they are bound to it.
func credentialsOptions(cfg *Config, namespace string) []mcpserverPkg.CredentialsOption {
	return []mcpserverPkg.CredentialsOption{
		mcpserverPkg.WithSecretBindingMode(api.SecretBindingMode(cfg.MusterConfig.SecretBinding.Mode)),
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

// warnSecretBindingNotEnforced logs at start that secretBinding.mode is not
// enforce: an MCPServer can still have muster read and send any unbound
// Secret muster can get, other than the protected ones.
func warnSecretBindingNotEnforced(mode string) {
	if mode == config.SecretBindingModeEnforce {
		return
	}
	logging.Warn("SecretCredentials", "secretBinding.mode is %s: muster still reads a Secret an MCPServer names that is not bound to it. "+
		"Bind the Secrets the warnings name, then set secretBinding.mode: enforce", mode)
}
