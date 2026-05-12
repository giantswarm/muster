package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
)

// CreateEvent creates a Kubernetes Event for the given object.
func (k *Client) CreateEvent(ctx context.Context, obj client.Object, reason, message, eventType string) error {
	gvk, err := k.GroupVersionKindFor(obj)
	if err != nil {
		return fmt.Errorf("failed to get GroupVersionKind for object: %w", err)
	}

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: obj.GetName() + "-",
			Namespace:    obj.GetNamespace(),
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: gvk.GroupVersion().String(),
			Kind:       gvk.Kind,
			Name:       obj.GetName(),
			Namespace:  obj.GetNamespace(),
			UID:        obj.GetUID(),
		},
		Reason:         reason,
		Message:        message,
		Type:           eventType,
		Source:         corev1.EventSource{Component: sourceComponent},
		FirstTimestamp: metav1.NewTime(time.Now()),
		LastTimestamp:  metav1.NewTime(time.Now()),
		Count:          1,
	}

	if err := k.Create(ctx, event); err != nil {
		return fmt.Errorf("failed to create Kubernetes Event: %w", err)
	}

	return nil
}

// CreateEventForCRD creates a Kubernetes Event for a CRD by type, name, and namespace.
func (k *Client) CreateEventForCRD(ctx context.Context, crdType, name, namespace, reason, message, eventType string) error {
	var gvk schema.GroupVersionKind
	switch crdType {
	case kindMCPServer:
		gvk = musterv1alpha1.GroupVersion.WithKind(kindMCPServer)
	case kindWorkflow:
		gvk = musterv1alpha1.GroupVersion.WithKind(kindWorkflow)
	default:
		return fmt.Errorf("unsupported CRD type: %s", crdType)
	}

	// Best-effort UID lookup so the Event references the live object.
	var uid types.UID
	switch crdType {
	case kindMCPServer:
		if obj, err := k.GetMCPServer(ctx, name, namespace); err == nil {
			uid = obj.GetUID()
		}
	case kindWorkflow:
		if obj, err := k.GetWorkflow(ctx, name, namespace); err == nil {
			uid = obj.GetUID()
		}
	}

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: name + "-",
			Namespace:    namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: gvk.GroupVersion().String(),
			Kind:       gvk.Kind,
			Name:       name,
			Namespace:  namespace,
			UID:        uid,
		},
		Reason:         reason,
		Message:        message,
		Type:           eventType,
		Source:         corev1.EventSource{Component: sourceComponent},
		FirstTimestamp: metav1.NewTime(time.Now()),
		LastTimestamp:  metav1.NewTime(time.Now()),
		Count:          1,
	}

	if err := k.Create(ctx, event); err != nil {
		return fmt.Errorf("failed to create Kubernetes Event for %s %s/%s: %w", crdType, namespace, name, err)
	}

	return nil
}

// QueryEvents retrieves events from the Kubernetes Events API with filtering.
// Time-range and source-component filtering happen client-side because the
// Kubernetes field-selector API doesn't support those.
func (k *Client) QueryEvents(ctx context.Context, options api.EventQueryOptions) (*api.EventQueryResult, error) {
	eventList := &corev1.EventList{}
	listOptions := &client.ListOptions{}

	var fieldSelectors []string
	if options.ResourceType != "" {
		fieldSelectors = append(fieldSelectors, fmt.Sprintf("involvedObject.kind=%s", options.ResourceType))
	}
	if options.ResourceName != "" {
		fieldSelectors = append(fieldSelectors, fmt.Sprintf("involvedObject.name=%s", options.ResourceName))
	}
	if options.Namespace != "" {
		listOptions.Namespace = options.Namespace
	}
	if options.EventType != "" {
		fieldSelectors = append(fieldSelectors, fmt.Sprintf("type=%s", options.EventType))
	}

	if len(fieldSelectors) > 0 {
		listOptions.FieldSelector = fields.ParseSelectorOrDie(strings.Join(fieldSelectors, ","))
	}

	if err := k.List(ctx, eventList, listOptions); err != nil {
		return nil, fmt.Errorf("failed to list Kubernetes events: %w", err)
	}

	var results []api.EventResult
	for _, event := range eventList.Items {
		if event.Source.Component != sourceComponent {
			continue
		}

		result := k.convertKubernetesEvent(&event)

		if options.Since != nil && result.Timestamp.Before(*options.Since) {
			continue
		}
		if options.Until != nil && result.Timestamp.After(*options.Until) {
			continue
		}

		results = append(results, result)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Timestamp.After(results[j].Timestamp)
	})

	totalCount := len(results)

	initialResults := results
	if options.Limit > 0 && len(results) > options.Limit {
		initialResults = results[:options.Limit]
	}

	return &api.EventQueryResult{
		Events:     initialResults,
		TotalCount: totalCount,
	}, nil
}

// convertKubernetesEvent converts a Kubernetes Event to our EventResult format.
// Falls back through LastTimestamp → FirstTimestamp → CreationTimestamp because
// not every Event populates every timestamp field.
func (k *Client) convertKubernetesEvent(event *corev1.Event) api.EventResult {
	timestamp := event.LastTimestamp.Time
	if timestamp.IsZero() && !event.FirstTimestamp.Time.IsZero() {
		timestamp = event.FirstTimestamp.Time
	}
	if timestamp.IsZero() {
		timestamp = event.CreationTimestamp.Time
	}

	return api.EventResult{
		Timestamp: timestamp,
		Namespace: event.Namespace,
		InvolvedObject: api.ObjectReference{
			APIVersion: event.InvolvedObject.APIVersion,
			Kind:       event.InvolvedObject.Kind,
			Name:       event.InvolvedObject.Name,
			Namespace:  event.InvolvedObject.Namespace,
			UID:        string(event.InvolvedObject.UID),
		},
		Reason:  event.Reason,
		Message: event.Message,
		Type:    event.Type,
		Source:  event.Source.Component,
		Count:   event.Count,
	}
}
