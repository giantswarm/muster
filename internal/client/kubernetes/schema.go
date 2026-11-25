package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// openAPIDocument is the part of a group version's OpenAPI v3 document
// MCPServerSpecHasField reads.
type openAPIDocument struct {
	Components struct {
		Schemas map[string]openAPISchema `json:"schemas"`
	} `json:"components"`
}

type openAPISchema struct {
	Properties map[string]openAPISchema `json:"properties"`
	GVK        []struct {
		Group   string `json:"group"`
		Version string `json:"version"`
		Kind    string `json:"kind"`
	} `json:"x-kubernetes-group-version-kind"`
}

// MCPServerSpecHasField reports whether the MCPServer CRD the API server
// serves knows spec.<field>. A CRD that does not know a field prunes it on
// every write, so a setting written there is silently dropped. It reads the
// API server's published OpenAPI v3 schema of muster's group version, which
// discovery serves to every authenticated client (no read on
// customresourcedefinitions needed).
func (k *Client) MCPServerSpecHasField(_ context.Context, field string) (bool, error) {
	paths, err := k.discovery.OpenAPIV3().Paths()
	if err != nil {
		return false, fmt.Errorf("read the API server's OpenAPI v3 paths: %w", err)
	}
	gv := musterv1alpha1.GroupVersion
	path, ok := paths["apis/"+gv.Group+"/"+gv.Version]
	if !ok {
		return false, fmt.Errorf("the API server publishes no OpenAPI v3 schema for %s", gv.String())
	}
	raw, err := path.Schema(runtime.ContentTypeJSON)
	if err != nil {
		return false, fmt.Errorf("read the OpenAPI v3 schema of %s: %w", gv.String(), err)
	}
	return specHasField(raw, field)
}

// specHasField finds the MCPServer schema in an OpenAPI v3 document and
// reports whether its spec has field.
func specHasField(raw []byte, field string) (bool, error) {
	var doc openAPIDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false, fmt.Errorf("parse the OpenAPI v3 schema: %w", err)
	}
	gv := musterv1alpha1.GroupVersion
	for _, schema := range doc.Components.Schemas {
		for _, gvk := range schema.GVK {
			if gvk.Group == gv.Group && gvk.Version == gv.Version && gvk.Kind == kindMCPServer {
				_, ok := schema.Properties["spec"].Properties[field]
				return ok, nil
			}
		}
	}
	return false, fmt.Errorf("the OpenAPI v3 schema of %s has no %s", gv.String(), kindMCPServer)
}
