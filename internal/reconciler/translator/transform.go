package translator

import (
	"errors"

	v1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// Transform produces a Model for the given MCPServer name and spec. It is pure:
// the result depends only on the inputs and never performs I/O.
//
// For spec.Type == "stdio" the resulting Model carries a ShimRequest; the
// reconciler is expected to resolve the shim's listener Endpoint and inject
// Host and Port into the matching Backend before handing the Model to a
// ConfigEmitter.
func Transform(name string, spec v1alpha1.MCPServerSpec) (Model, error) {
	_ = name
	_ = spec
	return Model{}, errors.New("translator.Transform: not implemented")
}
