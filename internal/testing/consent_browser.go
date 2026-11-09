package testing

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

func approveClientConsent(ctx context.Context, browser *http.Client, target string) (string, error) {
	authorizeURL, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	response, err := browser.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusOK {
		action, consentID, err := clientConsentForm(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return "", err
		}
		actionURL, err := authorizeURL.Parse(action)
		if err != nil || actionURL.Scheme != authorizeURL.Scheme || actionURL.Host != authorizeURL.Host || actionURL.Path != authorizeURL.Path || actionURL.User != nil {
			return "", fmt.Errorf("consent action must target the authorization endpoint")
		}
		form := url.Values{"consent_id": {consentID}, "decision": {"approve"}}
		request, err = http.NewRequestWithContext(ctx, http.MethodPost, actionURL.String(), strings.NewReader(form.Encode()))
		if err != nil {
			return "", err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", authorizeURL.Scheme+"://"+authorizeURL.Host)
		response, err = browser.Do(request)
		if err != nil {
			return "", err
		}
		defer func() { _ = response.Body.Close() }()
	}
	if response.StatusCode != http.StatusFound && response.StatusCode != http.StatusSeeOther {
		return "", fmt.Errorf("authorization returned status %d instead of a redirect", response.StatusCode)
	}
	if response.Header.Get("Location") == "" {
		return "", fmt.Errorf("authorization redirect has no Location header")
	}
	location, err := authorizeURL.Parse(response.Header.Get("Location"))
	if err != nil {
		return "", err
	}
	return location.String(), nil
}

func clientConsentForm(body io.Reader) (string, string, error) {
	document, err := html.Parse(body)
	if err != nil {
		return "", "", err
	}
	for node := range document.Descendants() {
		if node.Type != html.ElementNode || node.Data != "form" {
			continue
		}
		action := htmlAttribute(node, "action")
		if !strings.EqualFold(htmlAttribute(node, "method"), http.MethodPost) {
			continue
		}
		for input := range node.Descendants() {
			if input.Type == html.ElementNode && input.Data == "input" && htmlAttribute(input, "name") == "consent_id" && htmlAttribute(input, "value") != "" {
				return action, htmlAttribute(input, "value"), nil
			}
		}
	}
	return "", "", fmt.Errorf("authorization page has no client consent form")
}

func htmlAttribute(node *html.Node, name string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}
