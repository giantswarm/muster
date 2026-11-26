package aggregator

import (
	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

func validateDiscoveredResource(metadata *ProtectedResourceMetadata, serverURL string) error {
	if metadata.Resource == "" {
		logging.Warn("AuthTools", "DEPRECATED: protected resource metadata for %s omits resource; advertise its exact MCP resource identifier before enforcement", serverURL)
		return nil
	}
	if err := pkgoauth.ValidateAdvertisedResource(metadata.Resource, serverURL); err != nil {
		if resourceError := pkgoauth.ValidateResourceURI(metadata.Resource); resourceError != nil {
			return resourceError
		}
		logging.Warn("AuthTools", "DEPRECATED: protected resource metadata resource %q is not on the origin of %q; verify the operator-managed backend and its private tunnel mapping; RFC 9728 resource matching is not enforced on this discovery path", metadata.Resource, serverURL)
		return nil
	}
	resource, err := pkgoauth.DeriveResourceURI(serverURL)
	if err != nil {
		return err
	}
	if metadata.Resource != resource {
		logging.Warn("AuthTools", "DEPRECATED: protected resource metadata resource %q does not exactly match %q; update the resource identifier before enforcement", metadata.Resource, resource)
	}
	return nil
}
