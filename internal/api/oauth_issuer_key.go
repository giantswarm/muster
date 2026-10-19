package api

import "strings"

// issuerKeyPrefix starts the issuer key of a server outside muster's
// namespace. The namespace follows, then issuerKeySeparator and the issuer.
// A namespace is a DNS label, so it holds no separator.
const (
	issuerKeyPrefix    = "ns:"
	issuerKeySeparator = ":"
)

// IssuerKey returns the key muster files a server's OAuth state under: the
// person's tokens and subject grants, the pin and its metadata, the client
// registration and the flow's state. A server in muster's namespace (empty
// namespace, see SplitMCPServerKey) keeps the plain issuer as its key, so
// nothing stored before changes. A server outside it gets
// "ns:<namespace>:<issuer>", so its sign-in shares nothing with muster's own
// servers or with another namespace that names the same issuer.
//
// The plain issuer is used only where muster calls the network: IssuerURL.
//
// A plain issuer that already reads as a key of another namespace is no
// issuer: a server of muster's namespace whose own metadata names
// "ns:org-a:https://..." gets "", so it can never reach org-a's sign-ins.
func IssuerKey(namespace, issuer string) string {
	if strings.HasPrefix(issuer, issuerKeyPrefix) {
		return ""
	}
	if namespace == "" || issuer == "" {
		return issuer
	}
	return issuerKeyPrefix + namespace + issuerKeySeparator + strings.TrimSuffix(issuer, "/")
}

// ServerIssuerKey is IssuerKey for a server key from MCPServerKey.
func ServerIssuerKey(serverKey, issuer string) string {
	namespace, _ := SplitMCPServerKey(serverKey)
	return IssuerKey(namespace, issuer)
}

// SplitIssuerKey returns the namespace and the plain issuer of a key from
// IssuerKey. The namespace is empty for a key of muster's namespace.
func SplitIssuerKey(key string) (namespace, issuer string) {
	rest, ok := strings.CutPrefix(key, issuerKeyPrefix)
	if !ok {
		return "", key
	}
	namespace, issuer, ok = strings.Cut(rest, issuerKeySeparator)
	if !ok || namespace == "" {
		return "", key
	}
	return namespace, issuer
}

// IssuerURL returns the plain issuer of a key from IssuerKey: the value muster
// discovers metadata at and compares an authorization response with.
func IssuerURL(key string) string {
	_, issuer := SplitIssuerKey(key)
	return issuer
}

// IsTenantIssuerKey reports whether a key from IssuerKey belongs to a server
// outside muster's namespace.
func IsTenantIssuerKey(key string) bool {
	namespace, _ := SplitIssuerKey(key)
	return namespace != ""
}
