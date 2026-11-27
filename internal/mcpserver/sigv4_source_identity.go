package mcpserver

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/giantswarm/muster/v5/internal/api"
)

// sigV4MachineSourceIdentity is the source identity of a request no person is
// behind, such as the initialize and tools/list muster sends on connect.
const sigV4MachineSourceIdentity = clientName

// sigV4SourceIdentityPattern is what STS accepts as a source identity: 2 to
// 256 characters of [\w+=,.@-]. The "aws:" prefix STS also refuses cannot
// match, because ':' is not in the set.
var sigV4SourceIdentityPattern = regexp.MustCompile(`^[\w+=,.@-]{2,256}$`)

// sigV4SourceIdentity returns the source identity for a request made on
// behalf of subject. A subject STS would refuse is an error rather than a
// rewritten value: the source identity is what a trust policy matches on and
// what CloudTrail records, so it has to be the subject unchanged.
func sigV4SourceIdentity(subject string) (string, error) {
	if subject == "" {
		return sigV4MachineSourceIdentity, nil
	}
	if !sigV4SourceIdentityPattern.MatchString(subject) {
		return "", fmt.Errorf("the caller's subject cannot be an AWS source identity: it must be 2 to 256 characters of letters, digits and _+=,.@-")
	}
	return subject, nil
}

// sourceIdentityCredentials assumes the role once per source identity, so each
// caller's requests sign with a session that carries their subject. STS fixes
// a session's source identity when the role is assumed, which is why the
// callers cannot share one session.
type sourceIdentityCredentials struct {
	// newProvider returns the credential provider for one source identity.
	newProvider func(sourceIdentity string) aws.CredentialsProvider
	// idle is how long an unused source identity's provider is kept.
	idle time.Duration
	now  func() time.Time

	mu        sync.Mutex
	providers map[string]*sourceIdentityProvider
}

type sourceIdentityProvider struct {
	provider aws.CredentialsProvider
	lastUsed time.Time
}

func newSourceIdentityCredentials(newProvider func(sourceIdentity string) aws.CredentialsProvider, idle time.Duration) *sourceIdentityCredentials {
	return &sourceIdentityCredentials{
		newProvider: newProvider,
		idle:        idle,
		now:         time.Now,
		providers:   map[string]*sourceIdentityProvider{},
	}
}

func (c *sourceIdentityCredentials) Retrieve(ctx context.Context) (aws.Credentials, error) {
	sourceIdentity, err := sigV4SourceIdentity(api.GetSubjectFromContext(ctx))
	if err != nil {
		return aws.Credentials{}, err
	}
	return c.providerFor(sourceIdentity).Retrieve(ctx)
}

func (c *sourceIdentityCredentials) providerFor(sourceIdentity string) aws.CredentialsProvider {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if entry, ok := c.providers[sourceIdentity]; ok {
		entry.lastUsed = now
		return entry.provider
	}
	for id, entry := range c.providers {
		if now.Sub(entry.lastUsed) > c.idle {
			delete(c.providers, id)
		}
	}
	entry := &sourceIdentityProvider{provider: c.newProvider(sourceIdentity), lastUsed: now}
	c.providers[sourceIdentity] = entry
	return entry.provider
}
