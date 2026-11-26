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
		return err
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
