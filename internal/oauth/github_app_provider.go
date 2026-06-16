package oauth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/giantswarm/mcp-oauth/providers/tokencache"
	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"
)

const (
	// githubAPIBaseURL is the default GitHub API base URL.
	githubAPIBaseURL = "https://api.github.com"

	// githubAppPrivateKeyDefault is the default key name within the secret.
	githubAppPrivateKeyDefault = "private-key"

	// appJWTExpiry is the lifetime of the App JWT sent to the GitHub API.
	// GitHub requires exp - iat <= 10 minutes; 8 minutes leaves headroom for
	// clock skew between muster and the GitHub API.
	appJWTExpiry = 8 * time.Minute

	// appJWTBackdate is applied to iat to tolerate minor clock skew.
	appJWTBackdate = 60 * time.Second

	// issuedTokenType is the RFC 8693 token-type URN for GitHub installation tokens.
	issuedTokenType = "urn:ietf:params:oauth:token-type:access_token" //nolint:gosec // G101: RFC 8693 token-type URN, not a credential
)

// githubAppProvider implements CredentialProvider for GitHub App installation tokens.
//
// Minting flow: build an RS256 App JWT (iss=AppID), POST it to the GitHub
// installations API to obtain a short-lived installation token, return the
// token as the brokered credential.
//
// Authorization is enforced upstream by mcp-oauth before Mint is called.
// The subject token and subject identity are not forwarded to GitHub —
// installation tokens are app-scoped, not user-delegated.
type githubAppProvider struct {
	target config.BrokerTargetConfig
	// cache holds minted installation tokens, keyed on (installationID, permissions-hash).
	// Shared across Mint calls via the BrokerExchanger lifetime — providers are
	// reconstructed per request so a provider-local cache would never hit.
	cache      *tokencache.Cache
	defaultNS  string
	httpClient *http.Client
}

// githubInstallationTokenRequest is the JSON body for POST .../access_tokens.
type githubInstallationTokenRequest struct {
	Repositories []string          `json:"repositories,omitempty"`
	Permissions  map[string]string `json:"permissions,omitempty"`
}

// githubInstallationTokenResponse is the relevant subset of the GitHub API response.
type githubInstallationTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// githubInstallationResponse is used when auto-discovering an installation ID.
type githubInstallationResponse struct {
	ID int64 `json:"id"`
}

func (p *githubAppProvider) Mint(ctx context.Context, req MintRequest) (*MintResult, error) {
	cfg := p.target.GithubApp
	if cfg == nil {
		return nil, fmt.Errorf("target %q has type github-app but no githubApp config", req.Target)
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = githubAPIBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// Cache key encodes the installation scope so separate permission sets get
	// separate entries.
	cacheKey, err := githubAppCacheKey(cfg)
	if err != nil {
		return nil, fmt.Errorf("computing cache key for target %q: %w", req.Target, err)
	}
	if hit := p.cache.Get(cacheKey); hit != nil {
		logging.Debug("GitHubApp", "cache hit for target=%s", req.Target)
		return &MintResult{
			AccessToken:     hit.AccessToken,
			IssuedTokenType: hit.IssuedTokenType,
			ExpiresAt:       hit.ExpiresAt,
			FromCache:       true,
		}, nil
	}

	rsaKey, err := p.loadPrivateKey(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("load private key for target %q: %w", req.Target, err)
	}

	appJWT, err := buildAppJWT(cfg.AppID, rsaKey)
	if err != nil {
		return nil, fmt.Errorf("build App JWT for target %q: %w", req.Target, err)
	}

	installationID, err := p.resolveInstallationID(ctx, cfg, baseURL, appJWT)
	if err != nil {
		return nil, fmt.Errorf("resolve installation ID for target %q: %w", req.Target, err)
	}

	token, expiresAt, err := p.mintInstallationToken(ctx, cfg, baseURL, appJWT, installationID)
	if err != nil {
		return nil, fmt.Errorf("mint installation token for target %q: %w", req.Target, err)
	}

	if expiresIn := int(time.Until(expiresAt).Seconds()); expiresIn > 0 {
		p.cache.Set(cacheKey, token, issuedTokenType, expiresIn)
	}

	return &MintResult{
		AccessToken:     token,
		IssuedTokenType: issuedTokenType,
		ExpiresAt:       expiresAt,
		FromCache:       false,
	}, nil
}

// loadPrivateKey fetches the RSA private key PEM from the configured secret.
func (p *githubAppProvider) loadPrivateKey(ctx context.Context, cfg *config.GithubAppTargetConfig) (*rsa.PrivateKey, error) {
	if cfg.PrivateKeyRef == nil {
		return nil, fmt.Errorf("privateKeyRef is required")
	}
	handler := api.GetSecretCredentialsHandler()
	if handler == nil {
		return nil, fmt.Errorf("no secret credentials handler registered (github-app private key requires Kubernetes mode)")
	}

	keyName := cfg.PrivateKeyRef.ClientIDKey
	if keyName == "" {
		keyName = githubAppPrivateKeyDefault
	}

	pemBytes, err := handler.LoadSecretKey(ctx, &api.ClientCredentialsSecretRef{
		Name:      cfg.PrivateKeyRef.Name,
		Namespace: cfg.PrivateKeyRef.Namespace,
	}, keyName, p.defaultNS)
	if err != nil {
		return nil, err
	}

	return parseRSAPrivateKey(pemBytes)
}

// parseRSAPrivateKey decodes the first PEM block and parses an RSA private key.
// Accepts PKCS#1 (RSA PRIVATE KEY) and PKCS#8 (PRIVATE KEY) formats.
func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no valid PEM block found in private key")
	}

	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing RSA PKCS#1 private key: %w", err)
		}
		return key, nil
	case "PRIVATE KEY":
		raw, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing PKCS#8 private key: %w", err)
		}
		rsaKey, ok := raw.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("PKCS#8 private key is %T, expected *rsa.PrivateKey", raw)
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block type %q; expected RSA PRIVATE KEY or PRIVATE KEY", block.Type)
	}
}

// buildAppJWT creates an RS256-signed GitHub App JWT.
// iss=appID, iat=now-backdate (clock skew), exp=now+appJWTExpiry.
func buildAppJWT(appID string, key *rsa.PrivateKey) (string, error) {
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		return "", fmt.Errorf("creating RS256 signer: %w", err)
	}

	now := time.Now()
	claims := josejwt.Claims{
		Issuer:   appID,
		IssuedAt: josejwt.NewNumericDate(now.Add(-appJWTBackdate)),
		Expiry:   josejwt.NewNumericDate(now.Add(appJWTExpiry)),
	}

	token, err := josejwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		return "", fmt.Errorf("signing App JWT: %w", err)
	}
	return token, nil
}

// resolveInstallationID returns the installation ID from config or discovers it
// via GET /repos/{owner}/{repo}/installation.
func (p *githubAppProvider) resolveInstallationID(ctx context.Context, cfg *config.GithubAppTargetConfig, baseURL, appJWT string) (string, error) {
	if cfg.InstallationID != "" {
		return cfg.InstallationID, nil
	}
	if cfg.Owner == "" || cfg.Repo == "" {
		return "", fmt.Errorf("either installationId or both owner and repo must be set")
	}

	url := fmt.Sprintf("%s/repos/%s/%s/installation", baseURL, cfg.Owner, cfg.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("building installation discovery request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("GET %s returned %d: %s", url, resp.StatusCode, body)
	}

	var installation githubInstallationResponse
	if err := json.NewDecoder(resp.Body).Decode(&installation); err != nil {
		return "", fmt.Errorf("decoding installation response: %w", err)
	}
	if installation.ID == 0 {
		return "", fmt.Errorf("installation discovery returned empty ID")
	}
	return fmt.Sprintf("%d", installation.ID), nil
}

// mintInstallationToken POSTs to the GitHub installations API and returns the
// token and its expiry.
func (p *githubAppProvider) mintInstallationToken(
	ctx context.Context,
	cfg *config.GithubAppTargetConfig,
	baseURL, appJWT, installationID string,
) (string, time.Time, error) {
	url := fmt.Sprintf("%s/app/installations/%s/access_tokens", baseURL, installationID)

	body := githubInstallationTokenRequest{
		Repositories: cfg.Repositories,
		Permissions:  cfg.Permissions,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("marshaling token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", time.Time{}, fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, body)
	}

	var tokenResp githubInstallationTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", time.Time{}, fmt.Errorf("decoding token response: %w", err)
	}
	if tokenResp.Token == "" {
		return "", time.Time{}, fmt.Errorf("GitHub API returned empty token")
	}

	expiresAt, err := time.Parse(time.RFC3339, tokenResp.ExpiresAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("parsing expires_at %q: %w", tokenResp.ExpiresAt, err)
	}

	return tokenResp.Token, expiresAt, nil
}

// githubAppCacheKey returns a stable cache key for the given GitHub App config.
// Keyed on AppID + BaseURL + installationID (or owner/repo for auto-discovery)
// plus a hash of the requested repositories and permissions. AppID and BaseURL
// are included so that two distinct GitHub Apps targeting the same owner/repo
// (or coincidentally sharing an installation ID) never share a cache entry.
func githubAppCacheKey(cfg *config.GithubAppTargetConfig) (string, error) {
	installRef := cfg.InstallationID
	if installRef == "" {
		installRef = cfg.Owner + "/" + cfg.Repo
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = githubAPIBaseURL
	}

	// Stable JSON of scope — marshaling map[string]string is deterministic in
	// Go (sorted keys since 1.12).
	scope := struct {
		Repos       []string          `json:"r,omitempty"`
		Permissions map[string]string `json:"p,omitempty"`
	}{
		Repos:       cfg.Repositories,
		Permissions: cfg.Permissions,
	}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(scopeJSON)
	return fmt.Sprintf("github-app:%s:%s:%s:%x", cfg.AppID, baseURL, installRef, h), nil
}
