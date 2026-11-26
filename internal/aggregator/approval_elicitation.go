package aggregator

import (
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// approvalElicitations remembers which transport sessions were asked to open
// an approval page (the URL elicitation of a gated call), so that session,
// and only it, is sent notifications/elicitation/complete once the request
// is decided (approved or declined: the notification says only that the
// person is done, never what they decided). The client then retries; the
// outcome is muster's answer to that call.
//
// The zero value is ready to use. It is local to the replica: a decision made
// on a page served by another replica sends no notification, which the
// protocol allows (the notification is optional; the client keeps its own
// retry, and the portal its Done button).
type approvalElicitations struct {
	mu      sync.Mutex
	entries map[string][]approvalElicitationEntry // by approval request id
}

type approvalElicitationEntry struct {
	transportID   string
	elicitationID string
	expiresAt     time.Time
}

// add records that the transport session was asked to open the approval
// request's page with elicitationID, until expiresAt. A request without a
// transport session records nothing, and a session asked again is recorded
// once.
func (s *approvalElicitations) add(requestID, transportID, elicitationID string, expiresAt time.Time) {
	if transportID == "" || requestID == "" || elicitationID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, entries := range s.entries {
		kept := entries[:0]
		for _, e := range entries {
			if now.Before(e.expiresAt) {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(s.entries, id)
		} else {
			s.entries[id] = kept
		}
	}
	for _, e := range s.entries[requestID] {
		if e.transportID == transportID {
			return
		}
	}
	if s.entries == nil {
		s.entries = make(map[string][]approvalElicitationEntry)
	}
	s.entries[requestID] = append(s.entries[requestID], approvalElicitationEntry{transportID, elicitationID, expiresAt})
}

// take removes the request's elicitations and returns the unexpired ones.
func (s *approvalElicitations) take(requestID string) []sentElicitation {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var taken []sentElicitation
	for _, e := range s.entries[requestID] {
		if now.Before(e.expiresAt) {
			taken = append(taken, sentElicitation{transportID: e.transportID, elicitationID: e.elicitationID})
		}
	}
	delete(s.entries, requestID)
	return taken
}

// completeApprovalElicitations sends notifications/elicitation/complete for
// the elicitations of the decided approval request, to the transport sessions
// that were asked, and to no other.
func (a *AggregatorServer) completeApprovalElicitations(request *api.ApprovalRequest) {
	if a.mcpServer == nil || request == nil {
		return
	}
	for _, sent := range a.approvalElicitations.take(request.ID) {
		params := map[string]any{"elicitationId": sent.elicitationID}
		if err := a.mcpServer.SendNotificationToSpecificClient(sent.transportID, string(mcp.MethodNotificationElicitationComplete), params); err != nil {
			logging.Debug("Aggregator", "approval elicitation complete to session %s failed: %v",
				logging.TruncateIdentifier(sent.transportID), err)
		}
	}
}
