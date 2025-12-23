package reconciler

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWorkQueue_AddAndGet(t *testing.T) {
	q := NewQueue()

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	q.Add(req)

	if q.Len() != 1 {
		t.Errorf("expected queue length 1, got %d", q.Len())
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	got, ok := q.Get(ctx)
	if !ok {
		t.Fatal("expected to get item from queue")
	}

	if got.Name != req.Name || got.Type != req.Type {
		t.Errorf("got unexpected request: %+v", got)
	}

	// Mark as done
	q.Done(got)
}

func TestWorkQueue_Deduplication(t *testing.T) {
	q := NewQueue()

	req1 := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	req2 := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 2, // Updated attempt
	}

	q.Add(req1)
	q.Add(req2)

	// Should only have one item (deduplicated by key)
	if q.Len() != 1 {
		t.Errorf("expected queue length 1 after deduplication, got %d", q.Len())
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	got, ok := q.Get(ctx)
	if !ok {
		t.Fatal("expected to get item from queue")
	}

	// Should have the updated attempt number
	if got.Attempt != 2 {
		t.Errorf("expected attempt 2, got %d", got.Attempt)
	}

	q.Done(got)
}

func TestWorkQueue_DirtyRequeue(t *testing.T) {
	q := NewQueue()

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	q.Add(req)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Get the item (now processing)
	got, ok := q.Get(ctx)
	if !ok {
		t.Fatal("expected to get item from queue")
	}

	// Add same item while processing - should mark as dirty
	req2 := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 2,
	}
	q.Add(req2)

	// Queue should still appear empty since item is being processed
	if q.Len() != 0 {
		t.Errorf("expected queue length 0 while processing, got %d", q.Len())
	}

	// Mark as done - should re-add the dirty item
	q.Done(got)

	// Now should have the item back
	if q.Len() != 1 {
		t.Errorf("expected queue length 1 after done, got %d", q.Len())
	}

	// Get the re-queued item
	got2, ok := q.Get(ctx)
	if !ok {
		t.Fatal("expected to get dirty item from queue")
	}

	if got2.Attempt != 2 {
		t.Errorf("expected attempt 2, got %d", got2.Attempt)
	}

	q.Done(got2)
}

func TestWorkQueue_Shutdown(t *testing.T) {
	q := NewQueue()

	// Start a goroutine waiting for an item
	done := make(chan bool)
	go func() {
		ctx := context.Background()
		_, ok := q.Get(ctx)
		done <- ok
	}()

	// Give the goroutine time to start waiting
	time.Sleep(50 * time.Millisecond)

	// Shutdown should unblock the waiting Get
	q.Shutdown()

	select {
	case ok := <-done:
		if ok {
			t.Error("expected Get to return false after shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("Get did not unblock after shutdown")
	}
}

func TestWorkQueue_ConcurrentAccess(t *testing.T) {
	q := NewQueue()
	ctx := context.Background()

	var wg sync.WaitGroup
	numProducers := 5
	numItemsPerProducer := 10

	// Producers
	for i := 0; i < numProducers; i++ {
		wg.Add(1)
		go func(producerID int) {
			defer wg.Done()
			for j := 0; j < numItemsPerProducer; j++ {
				q.Add(ReconcileRequest{
					Type:    ResourceTypeMCPServer,
					Name:    "server-" + string(rune('A'+producerID)) + "-" + string(rune('0'+j)),
					Attempt: 1,
				})
			}
		}(i)
	}

	// Consumer
	consumed := 0
	consumerDone := make(chan struct{})
	go func() {
		for {
			timeoutCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			_, ok := q.Get(timeoutCtx)
			cancel()
			if !ok {
				break
			}
			consumed++
			q.Done(ReconcileRequest{}) // Simplified - just mark done
		}
		close(consumerDone)
	}()

	wg.Wait()
	time.Sleep(200 * time.Millisecond) // Allow consumer to finish
	q.Shutdown()

	<-consumerDone

	// Should have consumed at least some items
	if consumed == 0 {
		t.Error("expected to consume some items")
	}
}

func TestDelayedQueue_AddAfter(t *testing.T) {
	q := NewDelayedQueue()
	ctx := context.Background()

	start := time.Now()
	delay := 100 * time.Millisecond

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "delayed-server",
		Attempt: 1,
	}

	q.AddAfter(req, delay)

	// Should get the item after the delay
	got, ok := q.Get(ctx)
	elapsed := time.Since(start)

	if !ok {
		t.Fatal("expected to get item from queue")
	}

	if got.Name != req.Name {
		t.Errorf("got unexpected request: %+v", got)
	}

	if elapsed < delay {
		t.Errorf("item returned too quickly: %v < %v", elapsed, delay)
	}

	q.Done(got)
	q.Shutdown()
}

func TestDelayedQueue_CancelPending(t *testing.T) {
	q := NewDelayedQueue()

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "cancelled-server",
		Attempt: 1,
	}

	q.AddAfter(req, time.Hour) // Long delay

	// Shutdown should cancel pending timers
	q.Shutdown()

	// Verify queue is empty (item was never added)
	if q.Len() != 0 {
		t.Errorf("expected empty queue after shutdown, got %d", q.Len())
	}
}
