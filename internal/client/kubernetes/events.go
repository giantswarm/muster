package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/muster/internal/api"
)

// CreateEvent creates a Kubernetes Event for the given object via the
// EventBroadcaster, which aggregates duplicate events (Count) and rate-limits
// per source/object instead of writing one Event object per call.
func (k *Client) CreateEvent(ctx context.Context, obj client.Object, reason, message, eventType string) error {
	if k.eventRecorder == nil {
		return fmt.Errorf("event recorder not initialized")
	}
	// "%s" with message as an argument avoids interpreting any '%' the rendered
	// message may contain as a format directive.
	k.eventRecorder.Eventf(obj, eventType, reason, "%s", message)
	return nil
}

// CreateEventForCRD creates a Kubernetes Event for a CRD by type, name, and
// namespace. It best-effort loads the live object so the Event references the
// real UID, falling back to a minimal typed object carrying name/namespace.
func (k *Client) CreateEventForCRD(ctx context.Context, crdType, name, namespace, reason, message, eventType string) error {
	if k.eventRecorder == nil {
		return fmt.Errorf("event recorder not initialized")
	}

	factory, ok := crdFactories[crdType]
	if !ok {
		return fmt.Errorf("unsupported CRD type: %s", crdType)
	}

	obj := factory()
	if err := k.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
		// Live object not found (e.g. delete events): reference a minimal
		// object so the Event still carries the correct kind/name/namespace.
		obj = factory()
		obj.SetName(name)
		obj.SetNamespace(namespace)
	}

	k.eventRecorder.Eventf(obj, eventType, reason, "%s", message)
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
