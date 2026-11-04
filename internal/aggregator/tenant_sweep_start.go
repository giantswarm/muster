package aggregator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// At start, muster sweeps the sign-ins of every organization namespace the
// owner store holds a record for, against the MCPServer objects that exist
// then (giantswarm/muster#1772). The sweep retries of the process before
// lived in memory; on Valkey the record outlives them and still names the
// servers that went while muster was down, or while the store did not
// answer. A deleted namespace is never adopted again, so without this its
// people's tokens and grants would stay in the store for good.
//
// The sweep waits until the reconciler's informer has synced the MCPServer
// objects of every namespace, so muster has seen the existing servers before
// it compares the records with them; it never sweeps on a view of the
// servers that is not complete yet, which would erase the sign-ins of every
// tenant. The comparison itself lists the objects from the API server
// (tenantDefinitionUIDs), as at run time. Without that informer (filesystem
// mode, or MCPServers of muster's namespace only) there is nothing to sweep
// at start. With the in-memory stores the record starts empty, so the sweep
// finds nothing.
//
// Each namespace is swept as at run time: a namespace with no live recorded
// server loses everything; one with live servers keeps their sign-ins and
// loses only the issuer keys that gone servers alone used. A namespace whose
// sweep fails is handed to tenantSweepRetries. A store that cannot list the
// namespaces is asked again with the same backoff until it answers.

// sweepRecordedTenantsAtStart waits for listed to close, then sweeps every
// recorded namespace. It returns when muster stops (ctx ends).
func (a *AggregatorServer) sweepRecordedTenantsAtStart(ctx context.Context, listed <-chan struct{}) {
	select {
	case <-listed:
	case <-ctx.Done():
		return
	}
	namespaces, ok := a.recordedTenantNamespaces(ctx)
	if !ok {
		return
	}
	failed := 0
	for _, namespace := range namespaces {
		if namespace == "" {
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, tenantForgetTimeout)
		err := a.sweepRecordedTenant(attemptCtx, namespace)
		cancel()
		if err != nil {
			failed++
			a.tenantSweeps.schedule(namespace, "", err, func(ctx context.Context) error {
				return a.sweepGoneTenantServers(ctx, namespace, "")
			})
		}
	}
	logging.InfoWithAttrs("Aggregator", "tenant_signins_start_sweep_done",
		slog.Int("namespaces", len(namespaces)),
		slog.Int("retried", failed))
}

// recordedTenantNamespaces lists the namespaces of the owner store, asking
// again with backoff until it answers; ok is false when muster stops first.
func (a *AggregatorServer) recordedTenantNamespaces(ctx context.Context) (namespaces []string, ok bool) {
	backoff := tenantSweepInitialBackoff
	for attempt := 1; ; attempt++ {
		listCtx, cancel := context.WithTimeout(ctx, tenantForgetTimeout)
		namespaces, err := a.tenantOwners.Namespaces(listCtx)
		cancel()
		if err == nil {
			return namespaces, true
		}
		if ctx.Err() != nil {
			return nil, false
		}
		logging.WarnWithAttrs("Aggregator", "tenant_signins_start_sweep_failed",
			slog.Int("attempt", attempt),
			slog.Duration("retry_in", backoff),
			slog.String("error", err.Error()))
		if err := a.tenantSweeps.pause(ctx, backoff); err != nil {
			return nil, false
		}
		backoff = min(backoff*2, tenantSweepMaxBackoff)
	}
}

// sweepRecordedTenant sweeps a recorded namespace against the MCPServer
// objects that exist. It reads the record before it lists the objects, so a
// server adopted meanwhile is not in the record it compares and stays. A
// namespace with an empty record has nothing of a gone server to sweep.
func (a *AggregatorServer) sweepRecordedTenant(ctx context.Context, namespace string) error {
	owners, err := a.tenantOwners.Owners(ctx, namespace)
	if err != nil {
		return fmt.Errorf("read the owners of namespace %s: %w", namespace, err)
	}
	if len(owners) == 0 {
		return nil
	}
	live, err := tenantDefinitionUIDs(ctx, namespace)
	if err != nil {
		return fmt.Errorf("list the MCPServers of namespace %s: %w", namespace, err)
	}
	return a.sweepTenantNamespace(ctx, namespace, owners, live, "")
}
