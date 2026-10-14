package mcpserver

import (
	"github.com/giantswarm/muster/v5/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// convertCRDHeadersFromToAPI converts spec.headersFrom to its API form. Both
// carry references only.
func convertCRDHeadersFromToAPI(src []musterv1alpha1.MCPServerHeaderFrom) []api.MCPServerHeaderFrom {
	if src == nil {
		return nil
	}
	out := make([]api.MCPServerHeaderFrom, len(src))
	for i, h := range src {
		out[i] = api.MCPServerHeaderFrom{
			Name:      h.Name,
			ValueFrom: api.MCPServerValueSource(h.ValueFrom),
		}
	}
	return out
}

// convertAPIHeadersFromToCRD is the inverse of convertCRDHeadersFromToAPI.
func convertAPIHeadersFromToCRD(src []api.MCPServerHeaderFrom) []musterv1alpha1.MCPServerHeaderFrom {
	if src == nil {
		return nil
	}
	out := make([]musterv1alpha1.MCPServerHeaderFrom, len(src))
	for i, h := range src {
		out[i] = musterv1alpha1.MCPServerHeaderFrom{
			Name:      h.Name,
			ValueFrom: musterv1alpha1.MCPServerValueSource(h.ValueFrom),
		}
	}
	return out
}
