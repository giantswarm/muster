package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
)

// TestToolApprovalAPI calls a tool that asks and then uses muster's decision
// API (GET /approvals/{id} and POST /approvals/{id}/decision) with the
// current client's bearer, as a trusted client such as the portal would.
// Args: tool, arguments, decision ("approve" or "decline"; omitted: only the
// view), digest (overrides the one the view returned). Returns view_status,
// view (the JSON), view_error, decision_status, decision_error and status.
const TestToolApprovalAPI = "test_approval_api"

func (h *TestToolsHandler) handleApprovalAPI(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	tool, _ := args["tool"].(string)
	if tool == "" {
		return nil, fmt.Errorf("tool argument is required")
	}
	arguments, _ := args["arguments"].(map[string]interface{})
	decision, _ := args["decision"].(string)
	client := h.GetCurrentClient()
	if client == nil {
		return nil, fmt.Errorf("MCP client not available")
	}
	answer, err := client.CallTool(ctx, tool, arguments)
	text := fmt.Sprintf("%v", answer)
	if err != nil {
		text += " " + err.Error()
	}
	link := approvalLinkPattern.FindString(text)
	if link == "" {
		return nil, fmt.Errorf("the call did not ask for approval: %s", truncate(text, 400))
	}
	token := client.AccessToken()
	httpClient := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, target string, body any) (int, map[string]interface{}, error) {
		var reader io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				return 0, nil, err
			}
			reader = strings.NewReader(string(raw))
		}
		req, err := http.NewRequestWithContext(ctx, method, target, reader)
		if err != nil {
			return 0, nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		decoded := map[string]interface{}{}
		_ = json.Unmarshal(raw, &decoded)
		return resp.StatusCode, decoded, nil
	}

	result := map[string]interface{}{api.FieldSuccess: true, "link": link}
	status, view, err := call(http.MethodGet, link, nil)
	if err != nil {
		return nil, fmt.Errorf("viewing the approval failed: %w", err)
	}
	result["view_status"] = status
	result["view"] = view
	result["view_error"], _ = view["error"].(string)
	if decision == "" {
		return result, nil
	}
	digest, _ := view["digest"].(string)
	if override, ok := args["digest"].(string); ok {
		digest = override
	}
	status, decided, err := call(http.MethodPost, link+"/decision", map[string]string{"decision": decision, "digest": digest})
	if err != nil {
		return nil, fmt.Errorf("posting the decision failed: %w", err)
	}
	result["decision_status"] = status
	result["decision_error"], _ = decided["error"].(string)
	result["status"], _ = decided["status"].(string)
	return result, nil
}
