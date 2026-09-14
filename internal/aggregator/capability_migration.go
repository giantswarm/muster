package aggregator

import (
	"context"
	"log/slog"
	"time"

	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
	"github.com/giantswarm/muster/pkg/logging"
)

// capabilityMigrationTimeout bounds the one-off rewrite of a store that still
// carries capability documents inline in its session hashes. On the largest
// store seen (448 sessions, 462 MB) the pass is a few seconds; the bound keeps
// a stalled Valkey from pinning a goroutine forever.
const capabilityMigrationTimeout = 10 * time.Minute

// migrateCapabilityStore moves a Valkey-backed capability store to the
// content-addressed layout in the background (giantswarm/muster#1217). Safe to
// run on every start: a migrated store costs one SCAN. Failures are logged,
// never fatal — the store reads both layouts, so a partial migration is only a
// store that is larger than it needs to be until the next start.
func (a *AggregatorServer) migrateCapabilityStore(ctx context.Context) {
	store, ok := a.capabilityStore.(*oauthstore.ValkeyCapabilityStore)
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, capabilityMigrationTimeout)
		defer cancel()
		started := time.Now()
		report, err := store.MigrateInlineEntries(ctx)
		attrs := []slog.Attr{
			slog.Int("sessions", report.Sessions),
			slog.Int("migrated_fields", report.Migrated),
			slog.Int("documents", report.Documents),
			slog.Int("inline_bytes", report.InlineBytes),
			slog.Duration("took", time.Since(started)),
		}
		if err != nil {
			logging.WarnWithAttrs("CapabilityStore", "Capability store migration to content-addressed documents did not finish; it resumes on the next start",
				append(attrs, slog.String("error", err.Error()))...)
			return
		}
		if report.Migrated == 0 {
			logging.DebugWithAttrs("CapabilityStore", "Capability store already content-addressed", attrs...)
			return
		}
		logging.InfoWithAttrs("CapabilityStore", "Capability store migrated to content-addressed documents", attrs...)
	}()
}
