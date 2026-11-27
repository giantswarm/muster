package mcpserver

import (
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
)

// crdRequestMeta returns the entries a definition adds to params._meta:
// spec.requestMeta, or the deprecated spec.meta when only that is set.
// Admission refuses a definition that sets both; should one get through,
// requestMeta is the one applied.
func crdRequestMeta(spec musterv1alpha1.MCPServerSpec) map[string]string {
	if resolved, err := api.ResolveRequestMeta(spec.RequestMeta, spec.Meta); err == nil { //nolint:staticcheck // SA1019: the deprecated name is still read
		return resolved
	}
	return spec.RequestMeta
}

// requestedRequestMeta returns the spec.requestMeta and spec.meta a create or
// update request writes. An entry sent under the deprecated name is written
// under the current one. A request that sets both keeps both, so validation
// refuses it instead of picking one.
func requestedRequestMeta(requestMeta, meta map[string]string) (map[string]string, map[string]string) {
	if requestMeta != nil && meta != nil {
		return requestMeta, meta
	}
	if requestMeta != nil {
		return requestMeta, nil
	}
	return meta, nil
}
