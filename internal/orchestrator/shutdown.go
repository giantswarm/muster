package orchestrator

import (
	"context"
	"sync"

	"github.com/giantswarm/muster/v5/internal/services"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// Stop gracefully stops the orchestrator and every registered service within
// shutdownTimeout. It ends the maintenance loop first, so no retry restarts a
// server while it is being stopped, then stops the services in reverse
// dependency order (see shutdownWaves), and only then cancels the services'
// context: the aggregator's background routines run on it, and its Stop has to
// drain in-flight requests, stop the credential provider, close Valkey and
// release device-code leases before they go. A service whose Stop overruns
// the timeout is left behind, so a stuck one cannot hold the process past the
// pod's termination grace period.
func (o *Orchestrator) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), o.shutdownTimeout)
	defer cancel()

	if o.loopCancel != nil {
		o.loopCancel()
	}
	if !waitWithContext(ctx, &o.loopWg) {
		logging.Warn("Orchestrator", "Maintenance loop did not finish within %v, stopping services anyway", o.shutdownTimeout)
	}

	o.stopServices(ctx)

	if o.cancelFunc != nil {
		o.cancelFunc()
	}
	return nil
}

// stopServices stops the registered services wave by wave, the services of a
// wave concurrently. It gives up once ctx is done.
func (o *Orchestrator) stopServices(ctx context.Context) {
	for _, wave := range shutdownWaves(o.registry.GetAll()) {
		var wg sync.WaitGroup
		for _, svc := range wave {
			wg.Add(1)
			go func(svc services.Service) {
				defer wg.Done()
				if err := svc.Stop(ctx); err != nil {
					logging.Error("Orchestrator", err, "Failed to stop service: %s", svc.GetName())
				}
			}(svc)
		}
		if !waitWithContext(ctx, &wg) {
			logging.Warn("Orchestrator", "Services did not stop within %v, exiting without them", o.shutdownTimeout)
			return
		}
	}
	logging.Info("Orchestrator", "Stopped all services")
}

// shutdownWaves orders services for shutdown: each wave holds the services no
// remaining service depends on, so a service stops before the services it
// depends on. Besides the declared dependencies, the aggregator depends on
// every MCPServer: it stops first, draining the requests that call them and
// deregistering them itself. A dependency cycle ends in one last wave.
func shutdownWaves(svcs []services.Service) [][]services.Service {
	var mcpServers []string
	for _, svc := range svcs {
		if svc.GetType() == services.TypeMCPServer {
			mcpServers = append(mcpServers, svc.GetName())
		}
	}
	dependencies := func(svc services.Service) []string {
		if svc.GetType() == services.TypeAggregator {
			return append(svc.GetDependencies(), mcpServers...)
		}
		return svc.GetDependencies()
	}

	remaining := svcs
	var waves [][]services.Service
	for len(remaining) > 0 {
		needed := make(map[string]bool)
		for _, svc := range remaining {
			for _, dep := range dependencies(svc) {
				needed[dep] = true
			}
		}

		var wave, rest []services.Service
		for _, svc := range remaining {
			if needed[svc.GetName()] {
				rest = append(rest, svc)
			} else {
				wave = append(wave, svc)
			}
		}
		if len(wave) == 0 {
			wave, rest = rest, nil
		}
		waves = append(waves, wave)
		remaining = rest
	}
	return waves
}

// waitWithContext waits for wg and reports whether it finished before ctx
// was done.
func waitWithContext(ctx context.Context, wg *sync.WaitGroup) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}
