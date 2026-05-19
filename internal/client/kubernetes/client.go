package kubernetes

import (
	"context"
	"fmt"

	agw "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// CRD kinds used in switch statements and event references.
const (
	kindMCPServer = "MCPServer"
	kindWorkflow  = "Workflow"
)

// sourceComponent is the value muster sets on Event.Source.Component and the
// only value QueryEvents accepts when filtering events.
const sourceComponent = "muster"

// crdFactories maps a CRD kind string to a constructor for an empty typed
// object.
var crdFactories = map[string]func() client.Object{
	kindMCPServer: func() client.Object { return &musterv1alpha1.MCPServer{} },
	kindWorkflow:  func() client.Object { return &musterv1alpha1.Workflow{} },
}

// Client is a Kubernetes-API-backed implementation of the muster client
// interface. Per-domain CRUD methods live in sibling files (mcpserver.go,
// workflow.go, events.go); this file keeps the type, constructor, scheme,
// lifecycle methods, and discovery-based CRD validation.
type Client struct {
	client.Client
	scheme    *runtime.Scheme
	discovery discovery.DiscoveryInterface
}

// New returns a Kubernetes-backed Client for the given REST config. CRD
// presence is validated at construction so callers fail fast if the cluster
// hasn't installed muster's CRDs yet.
func New(config *rest.Config) (*Client, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(musterv1alpha1.AddToScheme(scheme))
	utilruntime.Must(agw.AddToScheme(scheme))
	utilruntime.Must(gwv1.Install(scheme))

	k8sClient, err := client.New(config, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Discovery validates CRD presence without requiring namespaced list
	// permissions on the muster CRDs — needed when muster runs with
	// namespace-scoped RBAC.
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	c := &Client{
		Client:    k8sClient,
		scheme:    scheme,
		discovery: discoveryClient,
	}

	if err := c.validateCRDs(context.Background()); err != nil {
		return nil, fmt.Errorf("CRD validation failed: %w", err)
	}

	return c, nil
}

// IsKubernetesMode returns true since this is the Kubernetes implementation.
func (k *Client) IsKubernetesMode() bool {
	return true
}

// Close performs cleanup for the Kubernetes client. Controller-runtime
// clients don't require explicit cleanup; this method exists for interface
// compatibility.
func (k *Client) Close() error {
	return nil
}

// Scheme returns the runtime scheme used by this client.
func (k *Client) Scheme() *runtime.Scheme {
	return k.scheme
}

// validateCRDs uses the discovery API to verify the muster API group is
// served and exposes the MCPServer kind. Discovery avoids requiring list/get
// permissions on the muster CRDs in any specific namespace.
func (k *Client) validateCRDs(ctx context.Context) error {
	gv := musterv1alpha1.GroupVersion.String()
	resourceList, err := k.discovery.ServerResourcesForGroupVersion(gv)
	if err != nil {
		if apierrors.IsNotFound(err) || discovery.IsGroupDiscoveryFailedError(err) {
			return fmt.Errorf("muster API group %s not registered: %w", gv, err)
		}
		return fmt.Errorf("failed to discover muster API group %s: %w", gv, err)
	}

	for _, r := range resourceList.APIResources {
		if r.Kind == kindMCPServer {
			return nil
		}
	}

	return fmt.Errorf("MCPServer CRD not available in API group %s", gv)
}
