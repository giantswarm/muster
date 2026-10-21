package chatgptlogin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

const (
	// CodeLifetime is how long a user code is polled for before it is given
	// up, the Codex CLI's limit.
	CodeLifetime = 15 * time.Minute

	// defaultInterval is the poll interval when OpenAI names none.
	defaultInterval = 5 * time.Second

	// maxResponseBytes bounds a device-auth response body.
	maxResponseBytes = 1 << 20
)

// ErrNotEnabled is the answer to a user-code request OpenAI refuses with 404:
// device-code sign-in is turned off for the ChatGPT account or workspace.
var ErrNotEnabled = errors.New("device-code sign-in is not enabled for this ChatGPT account: " +
	"enable it in ChatGPT under Settings → Security (\"Enable device code authorization for Codex\"; " +
	"for a workspace, its admin enables it in the workspace permissions), then connect again")

// ErrExpired is the end of a code that was not entered within CodeLifetime.
var ErrExpired = errors.New("the ChatGPT sign-in code expired before it was entered: connect again for a new code")

// Client signs a person in to ChatGPT with the device-code flow.
type Client struct {
	// Issuer is OpenAI's authorization server, https://auth.openai.com.
	Issuer string
	// ClientID is the public client signed in as, the Codex CLI's.
	ClientID string
	// TokenEndpoint is where the authorization code is redeemed. Default:
	// Issuer + /oauth/token.
	TokenEndpoint string
	// HTTP is the client of every request. Default: a client with
	// pkgoauth.DefaultHTTPTimeout.
	HTTP *http.Client
	// Wait sleeps between polls; it returns the context's error when the
	// context ends first. Default: a timer. Tests replace it.
	Wait func(ctx context.Context, d time.Duration) error
	// Now is the clock the code's expiry is read on. Default: time.Now.
	Now func() time.Time
}

// Code is an outstanding user code.
type Code struct {
	// UserCode is what the person enters at VerificationURL.
	UserCode string
	// VerificationURL is OpenAI's device page.
	VerificationURL string
	// ExpiresAt is when the code is given up.
	ExpiresAt time.Time

	deviceAuthID string
	interval     time.Duration
}

// RequestCode asks OpenAI for a user code. A 404 is ErrNotEnabled.
func (c *Client) RequestCode(ctx context.Context) (*Code, error) {
	var answer struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Usercode     string          `json:"usercode"`
		Interval     json.RawMessage `json:"interval"`
	}
	status, err := c.postJSON(ctx, c.apiURL("/deviceauth/usercode"), map[string]string{"client_id": c.ClientID}, &answer)
	if err != nil {
		return nil, fmt.Errorf("request a ChatGPT sign-in code: %w", err)
	}
	switch {
	case status == http.StatusNotFound:
		return nil, ErrNotEnabled
	case status != http.StatusOK:
		return nil, fmt.Errorf("request a ChatGPT sign-in code: OpenAI answered %d", status)
	}
	userCode := answer.UserCode
	if userCode == "" {
		userCode = answer.Usercode
	}
	if answer.DeviceAuthID == "" || userCode == "" {
		return nil, errors.New("request a ChatGPT sign-in code: OpenAI's answer carries no code")
	}
	return &Code{
		UserCode:        userCode,
		VerificationURL: strings.TrimSuffix(c.Issuer, "/") + "/codex/device",
		ExpiresAt:       c.now().Add(CodeLifetime),
		deviceAuthID:    answer.DeviceAuthID,
		interval:        parseInterval(answer.Interval),
	}, nil
}

// Await polls until the person entered the code, then redeems the
// authorization code for the person's tokens. It returns ErrExpired once the
// code's lifetime is over, the context's error when it ends first, and any
// other answer of OpenAI's as an error.
func (c *Client) Await(ctx context.Context, code *Code) (*pkgoauth.Token, error) {
	var answer struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	request := map[string]string{"device_auth_id": code.deviceAuthID, "user_code": code.UserCode}
	for {
		status, err := c.postJSON(ctx, c.apiURL("/deviceauth/token"), request, &answer)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("poll the ChatGPT sign-in: %w", err)
		}
		if status == http.StatusOK {
			break
		}
		if status != http.StatusForbidden && status != http.StatusNotFound {
			return nil, fmt.Errorf("poll the ChatGPT sign-in: OpenAI answered %d", status)
		}
		remaining := code.ExpiresAt.Sub(c.now())
		if remaining <= 0 {
			return nil, ErrExpired
		}
		if err := c.wait(ctx, min(code.interval, remaining)); err != nil {
			return nil, err
		}
	}
	if answer.AuthorizationCode == "" || answer.CodeVerifier == "" {
		return nil, errors.New("poll the ChatGPT sign-in: OpenAI's answer carries no authorization code")
	}

	issuer := strings.TrimSuffix(c.Issuer, "/")
	token, err := pkgoauth.NewClient(pkgoauth.WithHTTPClient(c.httpClient())).ExchangeCode(ctx,
		c.tokenEndpoint(), answer.AuthorizationCode, issuer+"/deviceauth/callback", c.ClientID, "", answer.CodeVerifier, "")
	if err != nil {
		return nil, fmt.Errorf("redeem the ChatGPT sign-in: %w", err)
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		return nil, errors.New("redeem the ChatGPT sign-in: OpenAI's answer carries no access or refresh token")
	}
	token.Issuer = issuer
	return token, nil
}

// AccountID returns the ChatGPT account id of an ID token, the
// https://api.openai.com/auth claim's chatgpt_account_id codex reads; "" when
// the token carries none.
func AccountID(idToken string) string {
	if idToken == "" {
		return ""
	}
	var claims struct {
		jwt.RegisteredClaims
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if _, _, err := jwt.NewParser().ParseUnverified(idToken, &claims); err != nil {
		return ""
	}
	return claims.Auth.AccountID
}

// postJSON posts body as JSON and decodes a 200 answer into out. It returns
// the status; a non-200 answer is not an error.
func (c *Client) postJSON(ctx context.Context, url string, body, out any) (int, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return 0, fmt.Errorf("decode OpenAI's answer: %w", err)
	}
	return resp.StatusCode, nil
}

// parseInterval reads OpenAI's poll interval, seconds as a string or a
// number.
func parseInterval(raw json.RawMessage) time.Duration {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	seconds, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || seconds <= 0 {
		return defaultInterval
	}
	return time.Duration(seconds) * time.Second
}

func (c *Client) apiURL(path string) string {
	return strings.TrimSuffix(c.Issuer, "/") + "/api/accounts" + path
}

func (c *Client) tokenEndpoint() string {
	if c.TokenEndpoint != "" {
		return c.TokenEndpoint
	}
	return strings.TrimSuffix(c.Issuer, "/") + "/oauth/token"
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: pkgoauth.DefaultHTTPTimeout}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if c.Wait != nil {
		return c.Wait(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
