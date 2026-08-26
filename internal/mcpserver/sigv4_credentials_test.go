package mcpserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// credentialFailureMessage stands in for what STS says when a role is not
	// there yet, or its trust policy is wrong.
	credentialFailureMessage = "role not deployed yet"

	// neverSucceeds is a succeedAfter high enough that the provider only ever
	// fails, for tests that just need a failing credential source.
	neverSucceeds = 1 << 30
)

// countingCredentials records how often the wrapped provider is entered, and
// fails until succeedAfter calls have been made.
type countingCredentials struct {
	calls        int
	succeedAfter int
}

func (c *countingCredentials) Retrieve(context.Context) (aws.Credentials, error) {
	c.calls++
	if c.calls <= c.succeedAfter {
		return aws.Credentials{}, errors.New(credentialFailureMessage)
	}
	return aws.Credentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}, nil
}

// TestBackoffCredentialsProviderCapsTheErrorRate covers the case the AWS
// credentials cache does not: it caches successes only, so a misconfigured role
// would otherwise mean one AssumeRole per reconnect, and the continuous-listening
// GET reconnects once a second.
func TestBackoffCredentialsProviderCapsTheErrorRate(t *testing.T) {
	inner := &countingCredentials{succeedAfter: neverSucceeds}
	clock := time.Unix(1_700_000_000, 0).UTC()

	provider := newBackoffCredentialsProvider(inner, 30*time.Second)
	provider.now = func() time.Time { return clock }

	// The first call reaches the provider and fails.
	_, err := provider.Retrieve(context.Background())
	require.ErrorContains(t, err, credentialFailureMessage)
	assert.Equal(t, 1, inner.calls)

	// Everything inside the backoff replays that error without another attempt.
	for range 100 {
		_, err = provider.Retrieve(context.Background())
		require.ErrorContains(t, err, credentialFailureMessage)
	}
	assert.Equal(t, 1, inner.calls, "the provider must not be re-entered during the backoff")

	// Once it expires, the next call tries again.
	clock = clock.Add(31 * time.Second)
	_, err = provider.Retrieve(context.Background())
	require.Error(t, err)
	assert.Equal(t, 2, inner.calls)
}

func TestBackoffCredentialsProviderClearsAfterSuccess(t *testing.T) {
	inner := &countingCredentials{succeedAfter: 1}
	clock := time.Unix(1_700_000_000, 0).UTC()

	provider := newBackoffCredentialsProvider(inner, 30*time.Second)
	provider.now = func() time.Time { return clock }

	_, err := provider.Retrieve(context.Background())
	require.Error(t, err)

	clock = clock.Add(31 * time.Second)
	creds, err := provider.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "AKID", creds.AccessKeyID)

	// A success resets the state, so the next call is not held back.
	creds, err = provider.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "AKID", creds.AccessKeyID)
	assert.Equal(t, 3, inner.calls)
}

// TestSigV4CredentialsBacksOffOnBothBranches pins that the error path is capped
// whether or not a role is assumed. The base chain is an STS call too —
// AssumeRoleWithWebIdentity in a pod — and LoadDefaultConfig caches it for
// successes only, so wrapping just the assume-role branch would leave the
// root-account entry uncapped.
func TestSigV4CredentialsBacksOffOnBothBranches(t *testing.T) {
	for _, roleARN := range []string{"", "arn:aws:iam::123456789012:role/ExampleReadOnlyRole"} {
		name := "with a roleArn"
		if roleARN == "" {
			name = "without a roleArn"
		}
		t.Run(name, func(t *testing.T) {
			provider, err := sigV4Credentials(context.Background(), testSigV4Region, roleARN)
			require.NoError(t, err)
			assert.IsType(t, &backoffCredentialsProvider{}, provider)
		})
	}
}
