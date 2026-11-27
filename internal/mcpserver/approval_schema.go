package mcpserver

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// An MCPServer CRD older than approval settings does not know spec.approval,
// and the API server prunes the field on every write: a connector's Ask and
// Block would silently be dropped and every tool would run. muster refuses
// instead (the PRD's "a CRD that doesn't know spec.approval makes muster
// refuse to enforce rather than run everything"): while the served CRD lacks
// the field, server tool calls are refused with the reason, and the write
// tools refuse to store settings. Every write of settings is read back too.

// approvalSchemaRecheck is how soon a CRD found without spec.approval, or
// one muster could not read, is checked again: an upgraded CRD is picked up
// without a restart. A CRD that knows the field is not checked again.
const approvalSchemaRecheck = 30 * time.Second

// specFieldChecker is the Kubernetes client's check of the served CRD.
type specFieldChecker interface {
	MCPServerSpecHasField(ctx context.Context, field string) (bool, error)
}

// approvalSchemaCache remembers the last check.
type approvalSchemaCache struct {
	mu        sync.Mutex
	known     bool
	problem   string
	checkedAt time.Time
}

// ApprovalSchemaProblem implements api.MCPServerApprovalSchema: "" when the
// served MCPServer CRD knows spec.approval (always in filesystem mode), else
// why muster cannot trust approval settings.
func (a *Adapter) ApprovalSchemaProblem(ctx context.Context) string {
	checker, ok := a.client.(specFieldChecker)
	if !ok || a.client == nil || !a.client.IsKubernetesMode() {
		return ""
	}
	c := &a.approvalSchema
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.known {
		return ""
	}
	now := clock.Now()
	if !c.checkedAt.IsZero() && now.Sub(c.checkedAt) < approvalSchemaRecheck {
		return c.problem
	}
	c.checkedAt = now
	known, err := checker.MCPServerSpecHasField(ctx, "approval")
	switch {
	case err != nil:
		c.problem = fmt.Sprintf("muster cannot confirm that the MCPServer CRD knows spec.approval (%v), so it cannot tell which tools ask or are blocked", err)
	case !known:
		c.problem = "the MCPServer CRD in this cluster does not know spec.approval (it predates approval settings, and the API server drops them), so muster cannot tell which tools ask or are blocked: upgrade the CRD from the muster chart's crds/ directory"
	default:
		c.known, c.problem = true, ""
		return ""
	}
	logging.Warn("MCPServer", "Approval settings cannot be trusted: %s", c.problem)
	return c.problem
}

// verifyApprovalStored reads the server back after a write of approval
// settings and refuses when what is stored differs from what was written:
// an API server whose CRD prunes spec.approval accepts the write and drops
// the settings.
func (a *Adapter) verifyApprovalStored(ctx context.Context, key string, written *musterv1alpha1.MCPServerApproval) *api.CallToolResult {
	namespace, name := api.MCPServerObject(key, a.namespace)
	stored, err := a.client.GetMCPServer(ctx, name, namespace)
	if err != nil {
		refused, _ := simpleError(fmt.Sprintf("MCP server '%s' was written, but muster could not read its approval settings back to confirm them: %v", key, err))
		return refused
	}
	if sameApproval(stored.Spec.Approval, written) {
		return nil
	}
	a.approvalSchema.forget()
	logging.Warn("MCPServer", "The approval settings written to MCP server %s were not stored: the API server dropped spec.approval", key)
	refused, _ := simpleError(fmt.Sprintf("the approval settings of MCP server '%s' were not stored: the API server dropped spec.approval, so the MCPServer CRD in this cluster predates approval settings. Upgrade the CRD from the muster chart's crds/ directory and set the settings again with core_mcpserver_set_approval", key))
	return refused
}

// forget drops a positive check, so the next call checks the CRD again.
func (c *approvalSchemaCache) forget() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.known, c.checkedAt = false, time.Time{}
}

// sameApproval compares settings, nil equal to an empty object.
func sameApproval(a, b *musterv1alpha1.MCPServerApproval) bool {
	if a == nil || b == nil {
		empty := func(x *musterv1alpha1.MCPServerApproval) bool {
			return x == nil || (x.Rule == "" && len(x.Tools) == 0)
		}
		return empty(a) && empty(b)
	}
	return a.Rule == b.Rule && maps.Equal(a.Tools, b.Tools)
}
