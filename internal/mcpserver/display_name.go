package mcpserver

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
)

// setDisplayName sets the DisplayNameAnnotation to the trimmed name. A blank
// name sets no annotation.
func setDisplayName(server *musterv1alpha1.MCPServer, name string) {
	if name = strings.TrimSpace(name); name != "" {
		metav1.SetMetaDataAnnotation(&server.ObjectMeta, api.DisplayNameAnnotation, name)
	}
}

// updateDisplayName applies an update's displayName. Tri-state: nil keeps the
// annotation, a blank value removes it and any other value replaces it.
func updateDisplayName(server *musterv1alpha1.MCPServer, name *string) {
	if name == nil {
		return
	}
	if strings.TrimSpace(*name) == "" {
		delete(server.Annotations, api.DisplayNameAnnotation)
		return
	}
	setDisplayName(server, *name)
}
