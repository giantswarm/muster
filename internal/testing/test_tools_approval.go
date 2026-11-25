package testing

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"

	"github.com/giantswarm/muster/v5/internal/api"
)

// TestToolDecideApproval calls a tool that asks, opens the approval link
// muster answers with in a browser signed in through muster's identity
// provider (as the current user, or as signed_in_as), and, with decision
// approve or decline, presses that button. An identical call reuses the
// pending approval, so a scenario calls the tool, decides, and calls it again.
// Args: tool, arguments, decision ("approve", "decline", or omitted to only
// view the page), signed_in_as, redirect.
const TestToolDecideApproval = "test_decide_approval"

var (
	approvalLinkPattern  = regexp.MustCompile(`https?://[^\s"'\\<>]+/approvals/[A-Za-z0-9]+`)
	approvalTokenPattern = regexp.MustCompile(`name="token" value="([^"]+)"`)
)

// maxApprovalPageHops bounds the redirects to the rendered page: the page,
// muster's identity provider, muster's OAuth callback, the page with the
// code, the page again.
const maxApprovalPageHops = 8

func (h *TestToolsHandler) handleDecideApproval(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	tool, _ := args["tool"].(string)
	if tool == "" {
		return nil, fmt.Errorf("tool argument is required")
	}
	arguments, _ := args["arguments"].(map[string]interface{})
	decision, _ := args["decision"].(string)
	if decision != "" && decision != "approve" && decision != "decline" {
		return nil, fmt.Errorf("decision must be approve or decline, got %q", decision)
	}
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

	signedInAs, err := identitySignInPersonFromArgs(args)
	if err != nil {
		return nil, err
	}
	browser, err := newBrowser()
	if err != nil {
		return nil, err
	}
	open := link
	if redirect, _ := args["redirect"].(string); redirect != "" {
		open += "?redirect=" + url.QueryEscape(redirect)
	}
	status, body, err := h.openApprovalPage(ctx, browser, open, signedInAs)
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{
		api.FieldSuccess: true,
		"link":           link,
		"page_status":    status,
		"page":           pageText(body),
	}
	token := approvalTokenPattern.FindStringSubmatch(body)
	result["buttons"] = token != nil
	if decision == "" {
		return result, nil
	}
	if token == nil {
		return nil, fmt.Errorf("the approval page offers no decision (status %d): %s", status, truncate(pageText(body), 400))
	}
	form := url.Values{"token": {token[1]}, "decision": {decision}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, link, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := browser.Do(req)
	if err != nil {
		return nil, fmt.Errorf("posting the decision failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	decided, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	result["decision_status"] = resp.StatusCode
	result["decision_page"] = pageText(string(decided))
	result["location"] = resp.Header.Get("Location")
	return result, nil
}

// openApprovalPage follows the approval link through muster's sign-in to
// the rendered page.
func (h *TestToolsHandler) openApprovalPage(ctx context.Context, browser *http.Client, link string, signedInAs identitySignInPerson) (int, string, error) {
	start, err := url.Parse(link)
	if err != nil {
		return 0, "", err
	}
	next := link
	for range maxApprovalPageHops {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return 0, "", err
		}
		resp, err := browser.Do(req)
		if err != nil {
			return 0, "", fmt.Errorf("request to %s failed: %w", next, err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if resp.StatusCode < 300 || resp.StatusCode >= 400 {
			return resp.StatusCode, string(body), nil
		}
		location, err := start.Parse(resp.Header.Get("Location"))
		if err != nil {
			return 0, "", err
		}
		switch {
		case identitySignIn(start, location.String()):
			subject, err := signedInAs.subjectAt(location.String())
			if err != nil {
				return 0, "", err
			}
			if subject == "" {
				token := h.GetCurrentClient().AccessToken()
				if subject = h.loginSubjects[token]; subject == "" {
					if subject, err = pkgoauth.Subject(token); err != nil {
						return 0, "", fmt.Errorf("resolve test browser identity: %w", err)
					}
				}
			}
			if next, err = h.approveIdentitySignIn(location.String(), subject); err != nil {
				return 0, "", err
			}
		case sameOrigin(start, location.String()):
			next = location.String()
		default:
			return 0, "", fmt.Errorf("the approval page sent the browser off muster to %s", location.Host)
		}
	}
	return 0, "", fmt.Errorf("the approval link did not reach the page within %d redirects", maxApprovalPageHops)
}

var tagPattern = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<[^>]+>`)

// pageText is the page's visible text, whitespace collapsed.
func pageText(body string) string {
	return strings.Join(strings.Fields(tagPattern.ReplaceAllString(body, " ")), " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
