package testing

import (
	"context"
	"fmt"

	"github.com/giantswarm/muster/v5/internal/api"
)

// handleSetAPIServerReachable takes the API server away from the scenario's
// Kubernetes-mode instance (reachable: false -- the proxy closes, muster's
// watches end, its next request is refused) or gives it back (reachable:
// true). Neither touches the control plane itself, nor the other instances
// sharing it. args: reachable (bool, required).
func (h *TestToolsHandler) handleSetAPIServerReachable(_ context.Context, args map[string]interface{}) (interface{}, error) {
	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}
	reachable, ok := args["reachable"].(bool)
	if !ok {
		return nil, fmt.Errorf("reachable argument (true or false) is required")
	}
	if err := h.instanceManager.SetAPIServerReachable(h.currentInstance.ID, reachable); err != nil {
		return nil, err
	}
	message := "API server unreachable for this instance: its proxy is closed"
	if reachable {
		message = "API server reachable again for this instance"
	}
	return map[string]interface{}{
		api.FieldSuccess: true,
		api.FieldMessage: message,
		"reachable":      reachable,
		"address":        h.currentInstance.APIServerAddr,
	}, nil
}

// handlePatchCR applies a JSON merge patch to a CR of the instance's
// namespace -- the way kubectl patch --type merge does -- so the reconciler
// is driven by a real CR update. Nested objects merge, null removes a field.
// args: name (required), patch (object, required), kind (MCPServer, the
// default, or Workflow). Returns the object as stored afterwards.
func (h *TestToolsHandler) handlePatchCR(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}
	name, _ := args["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("name argument is required")
	}
	patch, ok := args["patch"].(map[string]interface{})
	if !ok || len(patch) == 0 {
		return nil, fmt.Errorf("patch argument (a non-empty object) is required")
	}
	kind, _ := args["kind"].(string)

	obj, err := h.instanceManager.PatchCR(ctx, h.currentInstance.ID, kind, name, patch)
	if err != nil {
		return nil, err
	}
	if h.debug {
		h.logger.Debug("Patched %s %s in namespace %s with %v\n", kind, name, h.currentInstance.Namespace, patch)
	}
	return crResult(obj, fmt.Sprintf("Patched %s '%s'", crKind(obj, kind), name)), nil
}

// handleGetCR reads a CR of the instance's namespace as the API server stores
// it -- metadata, spec and the status muster wrote -- so a scenario can
// assert with json_path on status.state, labels or a spec field. args: name
// (required), kind (MCPServer, the default, or Workflow).
func (h *TestToolsHandler) handleGetCR(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}
	name, _ := args["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("name argument is required")
	}
	kind, _ := args["kind"].(string)

	obj, err := h.instanceManager.GetCR(ctx, h.currentInstance.ID, kind, name)
	if err != nil {
		return nil, err
	}
	return crResult(obj, fmt.Sprintf("Read %s '%s'", crKind(obj, kind), name)), nil
}

// crKind names the kind of a stored object, falling back to what the caller
// asked for.
func crKind(obj map[string]interface{}, requested string) string {
	if kind, _ := obj["kind"].(string); kind != "" {
		return kind
	}
	if requested == "" {
		return "MCPServer"
	}
	return requested
}

// crResult flattens a stored CR into the shape scenarios assert on:
// metadata (name, namespace, labels, resourceVersion), spec and status.
func crResult(obj map[string]interface{}, message string) map[string]interface{} {
	metadata, _ := obj[keyMetadata].(map[string]interface{})
	result := map[string]interface{}{
		api.FieldSuccess: true,
		api.FieldMessage: message,
		"kind":           crKind(obj, ""),
		keyMetadata:      metadata,
		"spec":           obj["spec"],
	}
	if status, ok := obj["status"]; ok {
		result["status"] = status
	} else {
		result["status"] = map[string]interface{}{}
	}
	if metadata != nil {
		result["name"] = metadata["name"]
		result["namespace"] = metadata["namespace"]
		if labels, ok := metadata["labels"]; ok {
			result["labels"] = labels
		}
	}
	return result
}
