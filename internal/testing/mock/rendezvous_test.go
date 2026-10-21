package mock

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRendezvousReleasesTogether(t *testing.T) {
	r := newRendezvous(2)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer r.enter()()
			errs[i] = r.wait(time.Minute)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if r.max() < 2 {
		t.Fatalf("max in flight = %d, want at least 2", r.max())
	}
}

func TestRendezvousFailsAlone(t *testing.T) {
	r := newRendezvous(2)
	defer r.enter()()
	err := r.wait(time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "the most seen was 1") {
		t.Fatalf("error = %v, want a rendezvous failure naming 1 call", err)
	}
	// The failed call withdrew: a later pair still meets.
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.wait(time.Minute); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
