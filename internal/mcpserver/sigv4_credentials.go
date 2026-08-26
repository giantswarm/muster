package mcpserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	// sigV4AssumeRoleDuration is how long an assumed-role credential lasts.
	// The SDK default is 15 minutes, which for a family of eighteen servers
	// means eighteen refreshes an hour.
	sigV4AssumeRoleDuration = time.Hour

	// sigV4ExpiryWindow refreshes a credential this far ahead of expiry, so the
	// STS call happens before a request needs it rather than inside one. The
	// jitter spreads the refreshes of a family whose members were all primed in
	// the same startup second.
	sigV4ExpiryWindow       = 2 * time.Minute
	sigV4ExpiryWindowJitter = 0.5

	// sigV4CredentialErrorBackoff is how long a failed credential lookup is
	// remembered. aws.CredentialsCache caches successes only, so without this
	// every retry re-enters the provider, and an outer reconnect loop calling
	// Retrieve once a second turns a misconfigured role into a sustained STS
	// call rate across the whole family.
	sigV4CredentialErrorBackoff = 30 * time.Second
)

// sigV4RoleSessionName is the STS session name every assumed role carries. It
// tracks the MCP client name deliberately — one identity for muster on the wire
// — but is declared separately because the two have different constraints: an
// STS session name is limited to 64 characters of [\w+=,.@-], and renaming it
// rewrites CloudTrail attribution.
const sigV4RoleSessionName = clientName

var (
	baseAWSConfigMu    sync.Mutex
	baseAWSConfigCache *aws.Config
)

// baseAWSConfig loads the default AWS configuration once per process.
//
// In a deployed muster the default chain picks up the pod identity webhook's
// environment variables and performs AssumeRoleWithWebIdentity. That base
// identity is the same for every MCPServer, so loading it once keeps a family
// of eighteen servers from opening eighteen separate credential chains for it.
// LoadDefaultConfig already wraps what it resolves in a credentials cache, and
// that cache singleflights, so a startup burst collapses into one STS call.
//
// Only a success is cached. A failure is retried on the next call, because the
// causes are transient or externally fixable — a token file not yet projected, a
// malformed shared config — and a memoized error would keep every AWS server
// failing for the pod's lifetime. That rules out sync.OnceValues, which caches
// both.
//
// defaultRegion is a last resort, applied only when no region resolves from the
// environment or the shared configuration files. It has two jobs: it keeps the
// chain from probing IMDS for a region, which in a pod without instance
// metadata means a startup stall, and it gives the no-roleArn branch of
// sigV4Credentials an STS endpoint to reach — that branch signs with the base
// credentials directly, so it cannot pin a region of its own the way the
// assume-role branch does.
//
// Do not disable IMDS to achieve the same thing. The instance-role provider is
// the last branch of the credential chain and is built from the same IMDS
// client, so disabling it removes a way of authenticating, not just a region
// lookup — muster on an EC2 host would stop resolving credentials at all.
//
// The consequence of the parameter is that the cached config takes the region of
// whichever MCPServer connected first. That only decides a fallback nothing else
// supplied, and every server pins its own region downstream, so the effect is
// limited to which STS endpoint an unregioned environment reaches.
func baseAWSConfig(ctx context.Context, defaultRegion string) (aws.Config, error) {
	baseAWSConfigMu.Lock()
	defer baseAWSConfigMu.Unlock()

	if baseAWSConfigCache != nil {
		return *baseAWSConfigCache, nil
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithDefaultRegion(defaultRegion))
	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to load AWS configuration: %w", err)
	}

	baseAWSConfigCache = &cfg
	return cfg, nil
}

// backoffCredentialsProvider remembers a failed credential lookup for a short
// while and replays the error instead of re-entering the wrapped provider.
//
// aws.CredentialsCache deliberately caches only successful retrievals, which
// leaves the error path uncapped: an outer reconnect loop calling Retrieve once
// a second turns a misconfigured role into a sustained STS call rate. This caps
// that without hiding a recovery — once the backoff expires the next call goes
// through to the provider again.
type backoffCredentialsProvider struct {
	wrapped aws.CredentialsProvider
	backoff time.Duration
	// now is injectable so tests do not sleep.
	now func() time.Time

	mu         sync.Mutex
	lastErr    error
	retryAfter time.Time
}

func newBackoffCredentialsProvider(wrapped aws.CredentialsProvider, backoff time.Duration) *backoffCredentialsProvider {
	return &backoffCredentialsProvider{wrapped: wrapped, backoff: backoff, now: time.Now}
}

func (p *backoffCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	// A zero retryAfter is always in the past, so this one comparison covers
	// both "never failed" and "backoff expired".
	p.mu.Lock()
	if p.now().Before(p.retryAfter) {
		err := p.lastErr
		p.mu.Unlock()
		return aws.Credentials{}, err
	}
	p.mu.Unlock()

	// Deliberately unlocked: aws.CredentialsCache singleflights its own
	// retrievals, so concurrent misses already collapse into one STS call.
	// Holding the lock here would instead serialise cache hits behind an
	// in-flight round trip.
	creds, err := p.wrapped.Retrieve(ctx)

	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.lastErr, p.retryAfter = err, p.now().Add(p.backoff)
		return aws.Credentials{}, err
	}
	p.retryAfter = time.Time{}
	return creds, nil
}

// sigV4Credentials resolves the credential provider that signs requests for one
// MCPServer.
//
// With no roleARN the base credentials are used directly, which is how the
// server in the same account as muster's own identity signs. With a roleARN the
// base credentials assume it, one hop per MCPServer, cached for successes
// because every request on the transport retrieves credentials.
//
// Either way the result is wrapped for backoff. The base chain is an STS call
// too — AssumeRoleWithWebIdentity in a pod — and LoadDefaultConfig caches it
// for successes only, so the error path needs capping on both branches, not
// just the assume-role one.
func sigV4Credentials(ctx context.Context, region, roleARN string) (aws.CredentialsProvider, error) {
	cfg, err := baseAWSConfig(ctx, region)
	if err != nil {
		return nil, err
	}

	provider := cfg.Credentials
	if roleARN != "" {
		stsClient := sts.NewFromConfig(cfg, func(o *sts.Options) {
			// The base config's region is whichever server connected first, so
			// pin this server's own rather than inheriting it.
			o.Region = region
		})
		assumed := stscreds.NewAssumeRoleProvider(stsClient, roleARN,
			func(o *stscreds.AssumeRoleOptions) {
				o.RoleSessionName = sigV4RoleSessionName
				o.Duration = sigV4AssumeRoleDuration
			})
		provider = aws.NewCredentialsCache(assumed, func(o *aws.CredentialsCacheOptions) {
			o.ExpiryWindow = sigV4ExpiryWindow
			o.ExpiryWindowJitterFrac = sigV4ExpiryWindowJitter
		})
	}
	return newBackoffCredentialsProvider(provider, sigV4CredentialErrorBackoff), nil
}
