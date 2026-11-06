package reconciler

import (
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// WatchMCPServerScope sets the MCPServer objects muster serves (the
// configuration's mcpServerNamespaceSelector and mcpServerSelector). With a
// namespace selector the detector watches MCPServer objects of every
// namespace, and, when the selector reads labels, Namespace objects too: a
// namespace whose labels change has each of its servers reconciled, so they
// start or go as the selector now says. With a server selector, a change of
// an object's labels that changes whether it is selected is reconciled. Call
// it before Start.
func (d *KubernetesDetector) WatchMCPServerScope(namespaces, servers labels.Selector) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mcpServerNamespaces = namespaces
	d.mcpServerSelector = servers
	d.mcpServersInAllNamespaces = namespaces != nil
}

// watchesNamespaces reports whether the detector watches Namespace objects.
func (d *KubernetesDetector) watchesNamespaces() bool {
	return d.mcpServerNamespaces != nil && !d.mcpServerNamespaces.Empty()
}

// setupNamespaceInformer watches Namespace objects whose labels decide
// whether their servers are served.
func (d *KubernetesDetector) setupNamespaceInformer() error {
	informer, err := d.cache.GetInformer(d.ctx, &corev1.Namespace{})
	if err != nil {
		return err
	}
	registration, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldNS, oldOK := oldObj.(*corev1.Namespace)
			newNS, newOK := newObj.(*corev1.Namespace)
			if !oldOK || !newOK {
				return
			}
			if d.mcpServerNamespaces.Matches(labels.Set(oldNS.Labels)) != d.mcpServerNamespaces.Matches(labels.Set(newNS.Labels)) {
				d.reconcileNamespace(newNS.Name)
			}
		},
	})
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.informerRegistrations = append(d.informerRegistrations, registration)
	d.mu.Unlock()
	return nil
}

// selectionChanged reports whether an update moved an object in or out of
// mcpServerSelector.
func (d *KubernetesDetector) selectionChanged(oldObj, newObj client.Object) bool {
	if d.mcpServerSelector == nil {
		return false
	}
	return d.mcpServerSelector.Matches(labels.Set(oldObj.GetLabels())) != d.mcpServerSelector.Matches(labels.Set(newObj.GetLabels()))
}

// reconcileShadowedNamespaces reconciles the servers of the namespaces whose
// exposed names a server of muster's namespace can take (api.ShadowingServer):
// the namespace named by the part of its toolPrefix (else its name), or of its
// family name, before the first '_'. A server of another namespace gives way
// to it, whatever the order in which the two appear, and comes back once it
// goes.
func (d *KubernetesDetector) reconcileShadowedNamespaces(objects ...client.Object) {
	if !d.mcpServersInAllNamespaces {
		return
	}
	seen := make(map[string]bool)
	for _, obj := range objects {
		server, ok := obj.(*musterv1alpha1.MCPServer)
		if !ok || server.Namespace != d.namespace {
			continue
		}
		prefix := server.Spec.ToolPrefix
		if prefix == "" {
			prefix = server.Name
		}
		heads := []string{prefix}
		if server.Spec.Family != nil {
			heads = append(heads, server.Spec.Family.Name)
		}
		for _, head := range heads {
			namespace, _, _ := strings.Cut(head, "_")
			if namespace == "" || namespace == d.namespace || seen[namespace] {
				continue
			}
			seen[namespace] = true
			d.reconcileNamespace(namespace)
		}
	}
}

// reconcileNamespace emits an update for every MCPServer object of
// namespace in the cache.
func (d *KubernetesDetector) reconcileNamespace(namespace string) {
	if !d.synced.Load() {
		return
	}
	var list musterv1alpha1.MCPServerList
	if err := d.cache.List(d.ctx, &list, client.InNamespace(namespace)); err != nil {
		logging.Warn("KubernetesDetector", "Cannot list the MCPServers of namespace %s to reconcile them: %v", namespace, err)
		return
	}
	for i := range list.Items {
		d.sendChangeEvent(ChangeEvent{
			Type:      ResourceTypeMCPServer,
			Name:      list.Items[i].Name,
			Namespace: namespace,
			Operation: OperationUpdate,
			Timestamp: time.Now(),
			Source:    SourceKubernetes,
		})
	}
}

// setupScopeInformers sets up the Namespace informer when the namespace
// selector reads labels.
func (d *KubernetesDetector) setupScopeInformers() error {
	d.mu.RLock()
	watch := d.watchesNamespaces()
	d.mu.RUnlock()
	if !watch {
		return nil
	}
	return d.setupNamespaceInformer()
}
