package mcpserver

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
)

// errUpdateRefused ends handleMCPServerUpdate's retry when
// applyMCPServerUpdate refuses the request.
var errUpdateRefused = fmt.Errorf("update refused")

// applyMCPServerUpdate applies an update request onto the live server and
// validates the result. It returns the tool error refusing the update, or nil.
func (a *Adapter) applyMCPServerUpdate(existing *musterv1alpha1.MCPServer, req *api.MCPServerUpdateRequest) *api.CallToolResult {
	// Update common fields from request
	if req.Type != "" {
		existing.Spec.Type = req.Type
	}
	if req.ToolPrefix != "" {
		existing.Spec.ToolPrefix = req.ToolPrefix
	}
	if req.Family != nil {
		existing.Spec.Family = convertAPIFamilyToCRD(req.Family)
	}
	if req.Description != "" {
		existing.Spec.Description = req.Description
	}
	updateDisplayName(existing, req.DisplayName)
	annotations, err := api.ApplyTagsUpdate(existing.Annotations, req.Tags)
	if err != nil {
		return api.InvalidTagsErrorResult(err)
	}
	existing.Annotations = annotations
	// Tri-state: an omitted autoStart keeps the current value.
	if req.AutoStart != nil {
		existing.Spec.AutoStart = *req.AutoStart
	}
	// Tri-state: only an explicit suspended value changes the lifecycle state,
	// so unrelated updates don't silently resume a stopped server (issue #1057).
	if req.Suspended != nil {
		existing.Spec.Suspended = *req.Suspended
	}
	if req.RestartRequestedAt != nil {
		existing.Spec.RestartRequestedAt = &metav1.Time{Time: *req.RestartRequestedAt}
	}
	if req.Command != "" {
		existing.Spec.Command = req.Command
	}
	if req.Args != nil {
		existing.Spec.Args = req.Args
	}
	if req.URL != "" {
		existing.Spec.URL = req.URL
	}
	if req.Env != nil {
		existing.Spec.Env = req.Env
	}
	if req.Headers != nil {
		existing.Spec.Headers = req.Headers
	}
	if req.HeadersFrom != nil {
		existing.Spec.HeadersFrom = convertAPIHeadersFromToCRD(req.HeadersFrom)
	}
	if req.Meta != nil {
		existing.Spec.Meta = req.Meta
	}
	if req.Timeout > 0 {
		existing.Spec.Timeout = req.Timeout
	}
	if req.Progress != nil {
		existing.Spec.Progress = convertAPIProgressToCRD(req.Progress)
	}
	// Update auth configuration if provided
	if req.Auth != nil {
		existing.Spec.Auth = convertAPIAuthToCRD(req.Auth)
	}

	// Validate the updated definition (reuse existing CRD object)
	if err := a.validateMCPServer(existing); err != nil {
		refused, _ := simpleError(fmt.Sprintf("Invalid MCP server definition: %v", err))
		return refused
	}
	return nil
}
