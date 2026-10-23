package aggregator

import (
	"time"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// budgetedClient is a client whose budget can change while it is in use;
// every transport of internal/mcpserver is one (baseMCPClient.SetBudget).
type budgetedClient interface {
	SetBudget(timeout, maxCallDuration time.Duration)
}

// SetServerBudget gives every connection the pool holds to serverName the
// server's current spec.timeout and spec.progress.maxDuration. A pooled
// connection was built with the budget the server had then and outlives a
// change of its definition: without this, a changed spec.timeout reached a
// session's calls only once its connection was rebuilt, in practice after a
// muster restart. The connections stay open; the next operation on each runs
// under the new budget. Returns how many connections it updated.
func (p *SessionConnectionPool) SetServerBudget(serverName string, timeout, maxCallDuration time.Duration) int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	updated := 0
	for key, entry := range p.pool {
		if key.ServerName != serverName {
			continue
		}
		client, ok := entry.Client.(budgetedClient)
		if !ok {
			logging.Debug("ConnPool", "Pooled client of session=%s server=%s takes no budget change (%T)",
				logging.TruncateIdentifier(key.SessionID), serverName, entry.Client)
			continue
		}
		client.SetBudget(timeout, maxCallDuration)
		updated++
	}
	return updated
}
