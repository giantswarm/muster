package reconciler

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	toolscache "k8s.io/client-go/tools/cache"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// startupWorkflows is the number of Workflow resources on the installation
// where the initial listing overflowed the change-event channel.
const startupWorkflows = 289

// listThenWatch serves the initial listing through its list function: the fake watch
// does not stream it the way the reflector's WatchList mode expects.
type listThenWatch struct{ *toolscache.ListWatch }

func (listThenWatch) IsWatchListSemanticsUnSupported() bool { return true }

// TestKubernetesDetectorStartupListingDeliversEveryEvent runs a real informer
// over a listing of 289 Workflows into a channel sized like the manager's.
// Nobody reads the channel until the informer has synced, the worst case for
// the burst. Every listed Workflow and a spec change arriving during the
// listing reach the channel; none is dropped.
func TestKubernetesDetectorStartupListingDeliversEveryEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	detector, err := NewKubernetesDetector(nil, "default")
	if err != nil {
		t.Fatalf("NewKubernetesDetector: %v", err)
	}
	changeChan := make(chan ChangeEvent, 100)
	detector.ctx = ctx
	detector.running = true
	detector.changeChan = changeChan

	list := &musterv1alpha1.WorkflowList{ListMeta: metav1.ListMeta{ResourceVersion: "1"}}
	for i := range startupWorkflows {
		list.Items = append(list.Items, musterv1alpha1.Workflow{ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("workflow-%03d", i), Namespace: "default", Generation: 1, ResourceVersion: "1",
		}})
	}
	watcher := watch.NewFake()
	informer := toolscache.NewSharedIndexInformer(listThenWatch{&toolscache.ListWatch{
		ListWithContextFunc:  func(context.Context, metav1.ListOptions) (runtime.Object, error) { return list, nil },
		WatchFuncWithContext: func(context.Context, metav1.ListOptions) (watch.Interface, error) { return watcher, nil },
	}}, &musterv1alpha1.Workflow{}, 0, toolscache.Indexers{})
	if _, err := informer.AddEventHandler(detector.createEventHandler(ResourceTypeWorkflow)); err != nil {
		t.Fatalf("AddEventHandler: %v", err)
	}
	go informer.Run(ctx.Done())
	if !toolscache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		t.Fatal("informer did not sync")
	}

	changed := list.Items[0].DeepCopy()
	changed.Generation, changed.ResourceVersion = 2, "2"
	watcher.Modify(changed)

	creates := map[string]bool{}
	updated := false
	timeout := time.After(10 * time.Second)
	for len(creates) < startupWorkflows || !updated {
		select {
		case event := <-changeChan:
			switch event.Operation {
			case OperationCreate:
				creates[event.Name] = true
			case OperationUpdate:
				updated = event.Name == changed.Name
			}
		case <-timeout:
			t.Fatalf("received %d of %d created Workflows, update delivered: %v", len(creates), startupWorkflows, updated)
		}
	}
}
