package reconciler

import (
	"context"
	"sync"
	"time"
)

// requestKey generates a unique key for a reconcile request.
// This is used for deduplication and tracking across queue implementations.
func requestKey(req ReconcileRequest) string {
	if req.Namespace != "" {
		return string(req.Type) + "/" + req.Namespace + "/" + req.Name
	}
	return string(req.Type) + "/" + req.Name
}

// workQueue implements ReconcileQueue with deduplication and rate limiting.
type workQueue struct {
	mu sync.Mutex

	// queue holds requests in FIFO order
	queue []ReconcileRequest

	// processing tracks items currently being processed
	processing map[string]bool

	// dirty tracks items that need reprocessing
	dirty map[string]ReconcileRequest

	// cond is used for blocking Get operations
	cond *sync.Cond

	// shuttingDown indicates the queue is stopping
	shuttingDown bool
}

// NewQueue creates a new reconciliation queue.
func NewQueue() ReconcileQueue {
	q := &workQueue{
		queue:      make([]ReconcileRequest, 0),
		processing: make(map[string]bool),
		dirty:      make(map[string]ReconcileRequest),
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Add adds or updates a request in the queue.
func (q *workQueue) Add(req ReconcileRequest) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.shuttingDown {
		return
	}

	key := requestKey(req)

	// If already being processed, mark as dirty for reprocessing
	if q.processing[key] {
		q.dirty[key] = req
		return
	}

	// Check if already in queue
	for i, existing := range q.queue {
		if requestKey(existing) == key {
			// Update the existing entry
			q.queue[i] = req
			return
		}
	}

	// Add to queue
	q.queue = append(q.queue, req)
	q.cond.Signal()
}

// Get retrieves the next request, blocking if necessary.
func (q *workQueue) Get(ctx context.Context) (ReconcileRequest, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Wait for item or shutdown
	for len(q.queue) == 0 && !q.shuttingDown {
		// Check context before waiting
		select {
		case <-ctx.Done():
			return ReconcileRequest{}, false
		default:
		}

		// Use a separate goroutine to handle context cancellation.
		// The goroutine races to handle context cancellation vs normal wakeup.
		// Closing `done` ensures the goroutine exits regardless of which wins.
		//
		// The goroutine exits cleanly when either:
		// 1. The context is cancelled (broadcasts to wake us up)
		// 2. The done channel is closed (we woke up normally from Add/Shutdown)
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				q.mu.Lock()
				q.cond.Broadcast()
				q.mu.Unlock()
			case <-done:
				// Normal wakeup, goroutine exits cleanly
			}
		}()

		q.cond.Wait()
		close(done)

		// Check if context was cancelled
		select {
		case <-ctx.Done():
			return ReconcileRequest{}, false
		default:
		}
	}

	if q.shuttingDown && len(q.queue) == 0 {
		return ReconcileRequest{}, false
	}

	// Pop from queue
	req := q.queue[0]
	q.queue = q.queue[1:]

	// Mark as processing
	key := requestKey(req)
	q.processing[key] = true

	return req, true
}

// Done marks a request as completed.
func (q *workQueue) Done(req ReconcileRequest) {
	q.mu.Lock()
	defer q.mu.Unlock()

	key := requestKey(req)
	delete(q.processing, key)

	// Check if marked dirty during processing
	if dirtyReq, ok := q.dirty[key]; ok {
		delete(q.dirty, key)
		// Re-add to queue
		q.queue = append(q.queue, dirtyReq)
		q.cond.Signal()
	}
}

// Len returns the queue length.
func (q *workQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queue)
}

// Shutdown stops the queue.
func (q *workQueue) Shutdown() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.shuttingDown = true
	q.cond.Broadcast()
}

// delayedQueue wraps a queue with delayed requeue support.
type delayedQueue struct {
	queue      ReconcileQueue
	mu         sync.Mutex
	delayedMap map[string]*time.Timer
	stopCh     chan struct{}
}

// NewDelayedQueue creates a queue that supports delayed requeuing.
func NewDelayedQueue() *delayedQueue {
	return &delayedQueue{
		queue:      NewQueue(),
		delayedMap: make(map[string]*time.Timer),
		stopCh:     make(chan struct{}),
	}
}

// Add adds a request immediately.
func (d *delayedQueue) Add(req ReconcileRequest) {
	d.queue.Add(req)
}

// AddAfter adds a request after a delay.
func (d *delayedQueue) AddAfter(req ReconcileRequest, delay time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := requestKey(req)

	// Cancel any existing timer for this key
	if timer, ok := d.delayedMap[key]; ok {
		timer.Stop()
	}

	// Create new timer
	d.delayedMap[key] = time.AfterFunc(delay, func() {
		d.mu.Lock()
		delete(d.delayedMap, key)
		d.mu.Unlock()

		select {
		case <-d.stopCh:
			return
		default:
			d.queue.Add(req)
		}
	})
}

// Get retrieves the next request.
func (d *delayedQueue) Get(ctx context.Context) (ReconcileRequest, bool) {
	return d.queue.Get(ctx)
}

// Done marks a request as completed.
func (d *delayedQueue) Done(req ReconcileRequest) {
	d.queue.Done(req)
}

// Len returns the queue length.
func (d *delayedQueue) Len() int {
	return d.queue.Len()
}

// Shutdown stops the queue and cancels pending timers.
func (d *delayedQueue) Shutdown() {
	close(d.stopCh)

	d.mu.Lock()
	for _, timer := range d.delayedMap {
		timer.Stop()
	}
	d.delayedMap = make(map[string]*time.Timer)
	d.mu.Unlock()

	d.queue.Shutdown()
}

