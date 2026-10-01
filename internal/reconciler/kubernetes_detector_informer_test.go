package reconciler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// flakyCache is a cache whose GetInformer fails for a resource type until
// that type's remaining failures are used up.
type flakyCache struct {
	cache.Cache
	failures map[string]int
	calls    map[string]int
}

func (c *flakyCache) GetInformer(_ context.Context, obj client.Object, _ ...cache.InformerGetOption) (cache.Informer, error) {
	kind := "MCPServer"
	if _, ok := obj.(*musterv1alpha1.Workflow); ok {
		kind = "Workflow"
	}
	c.calls[kind]++
	if c.failures[kind] != 0 {
		c.failures[kind]--
		return nil, errors.New("failed to get restmapping: connection refused")
	}
	return &stubInformer{}, nil
}

type stubInformer struct{ cache.Informer }

func (i *stubInformer) AddEventHandler(toolscache.ResourceEventHandler) (toolscache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}

func newInformerTestDetector(t *testing.T, c cache.Cache, wait func(context.Context, time.Duration) error) *KubernetesDetector {
	t.Helper()
	d, err := NewKubernetesDetector(nil, "agent-platform")
	if err != nil {
		t.Fatalf("NewKubernetesDetector: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d.ctx, d.cancelFunc = ctx, cancel
	d.cache = c
	d.wait = wait
	for _, rt := range []ResourceType{ResourceTypeMCPServer, ResourceTypeWorkflow} {
		if err := d.AddResourceType(rt); err != nil {
			t.Fatalf("AddResourceType(%s): %v", rt, err)
		}
	}
	return d
}

// TestSetupInformersRetriesUntilEveryInformerIsSetUp pins that an informer
// whose setup fails is set up again, with growing backoff, instead of being
// skipped: a detector that returned with a missing informer let the pod go
// ready while it never observed a change to that type.
func TestSetupInformersRetriesUntilEveryInformerIsSetUp(t *testing.T) {
	c := &flakyCache{failures: map[string]int{"Workflow": 3}, calls: map[string]int{}}
	var pauses []time.Duration
	d := newInformerTestDetector(t, c, func(_ context.Context, d time.Duration) error {
		pauses = append(pauses, d)
		return nil
	})

	if err := d.setupInformers(); err != nil {
		t.Fatalf("setupInformers: %v", err)
	}

	if got := len(d.informerRegistrations); got != 2 {
		t.Errorf("informers registered = %d, want 2", got)
	}
	if c.calls["MCPServer"] != 1 {
		t.Errorf("MCPServer informer set up %d times, want once", c.calls["MCPServer"])
	}
	if c.calls["Workflow"] != 4 {
		t.Errorf("Workflow informer setup attempts = %d, want 4", c.calls["Workflow"])
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if len(pauses) != len(want) {
		t.Fatalf("pauses = %v, want %v", pauses, want)
	}
	for i := range want {
		if pauses[i] != want[i] {
			t.Errorf("pause %d = %s, want %s", i, pauses[i], want[i])
		}
	}
}

// TestSetupInformersBackoffIsCapped pins the 8 s cap of the retry cadence.
func TestSetupInformersBackoffIsCapped(t *testing.T) {
	c := &flakyCache{failures: map[string]int{"Workflow": 6}, calls: map[string]int{}}
	var last time.Duration
	d := newInformerTestDetector(t, c, func(_ context.Context, d time.Duration) error {
		last = d
		return nil
	})

	if err := d.setupInformers(); err != nil {
		t.Fatalf("setupInformers: %v", err)
	}
	if last != informerSetupMaxBackoff {
		t.Errorf("last pause = %s, want %s", last, informerSetupMaxBackoff)
	}
}

// TestSetupInformersFailsWhenContextEnds pins that a detector whose informer
// never comes up fails its start, naming the type and the last error, once
// its context ends.
func TestSetupInformersFailsWhenContextEnds(t *testing.T) {
	c := &flakyCache{failures: map[string]int{"Workflow": -1}, calls: map[string]int{}}
	d := newInformerTestDetector(t, c, func(context.Context, time.Duration) error {
		return context.Canceled
	})

	err := d.setupInformers()
	if err == nil {
		t.Fatal("setupInformers succeeded with the Workflow informer missing")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error %q does not wrap context.Canceled", err)
	}
	for _, want := range []string{"Workflow", "connection refused"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestSetupInformersUnsupportedTypeIsNotRetried pins that a type the detector
// has no informer for fails at once: no retry can fix it.
func TestSetupInformersUnsupportedTypeIsNotRetried(t *testing.T) {
	c := &flakyCache{failures: map[string]int{}, calls: map[string]int{}}
	d := newInformerTestDetector(t, c, func(context.Context, time.Duration) error {
		t.Fatal("an unsupported type was retried")
		return nil
	})
	d.resourceTypes[ResourceType("Unknown")] = true

	if err := d.setupInformers(); !errors.Is(err, errUnsupportedResourceType) {
		t.Errorf("setupInformers error = %v, want errUnsupportedResourceType", err)
	}
}

// TestManagerWatchesKubernetes pins the mode check that makes a failed
// reconciler start fatal in Kubernetes mode only.
func TestManagerWatchesKubernetes(t *testing.T) {
	for mode, want := range map[WatchMode]bool{
		WatchModeKubernetes: true,
		WatchModeFilesystem: false,
		WatchModeAuto:       false,
	} {
		if got := NewManager(ManagerConfig{Mode: mode}).WatchesKubernetes(); got != want {
			t.Errorf("WatchesKubernetes() in mode %q = %v, want %v", mode, got, want)
		}
	}
}
