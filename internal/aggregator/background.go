package aggregator

// goBackground runs f on a goroutine Stop waits for, and reports whether it
// did: none starts once Stop began waiting, so the wait group is never added
// to while Stop waits on it.
func (a *AggregatorServer) goBackground(f func()) bool {
	a.backgroundMu.Lock()
	if a.backgroundStopped {
		a.backgroundMu.Unlock()
		return false
	}
	a.wg.Add(1)
	a.backgroundMu.Unlock()
	go func() {
		defer a.wg.Done()
		f()
	}()
	return true
}

// stopBackground makes goBackground start nothing more; Stop calls it before
// it waits for the background goroutines.
func (a *AggregatorServer) stopBackground() {
	a.backgroundMu.Lock()
	a.backgroundStopped = true
	a.backgroundMu.Unlock()
}
