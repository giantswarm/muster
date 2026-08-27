package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// TestSigV4AndMetaSurviveTheConversionRoundTrip guards the plumbing the signing
// transport depends on. A dropped meta entry never fails: the AWS-hosted
// endpoint falls back to its own region and answers successfully about the
// wrong one, so nothing downstream can tell that the field was lost.
func TestSigV4AndMetaSurviveTheConversionRoundTrip(t *testing.T) {
	adapter := &Adapter{namespace: "muster"}

	crd := adapter.convertRequestToCRD(&api.MCPServerCreateRequest{
		Name: "root",
		Type: string(api.MCPServerTypeStreamableHTTP),
		URL:  testSigV4URL,
		Meta: map[string]string{"AWS_REGION": "us-east-1"},
		Auth: &api.MCPServerAuth{
			Type: api.MCPServerAuthTypeSigV4,
			SigV4: &api.MCPServerSigV4{
				Region:  testSigV4Region,
				Service: testSigV4Service,
				RoleARN: "arn:aws:iam::123456789012:role/ExampleReadOnlyRole",
			},
		},
	})

	assert.Equal(t, map[string]string{"AWS_REGION": "us-east-1"}, crd.Spec.Meta)
	require.NotNil(t, crd.Spec.Auth.SigV4)
	assert.Equal(t, musterv1alpha1.MCPServerSigV4{
		Region:  testSigV4Region,
		Service: testSigV4Service,
		RoleARN: "arn:aws:iam::123456789012:role/ExampleReadOnlyRole",
	}, *crd.Spec.Auth.SigV4)

	info := convertCRDToInfo(crd)

	assert.Equal(t, map[string]string{"AWS_REGION": "us-east-1"}, info.Meta)
	require.NotNil(t, info.Auth)
	require.NotNil(t, info.Auth.SigV4)
	assert.Equal(t, api.MCPServerAuthTypeSigV4, info.Auth.Type)
	assert.Equal(t, *crd.Spec.Auth.SigV4, *convertAPISigV4ToCRD(info.Auth.SigV4))

	// The update path shares convertAPIAuthToCRD with create, so a field that
	// reaches the CR one way cannot be dropped by the other.
	assert.Equal(t, crd.Spec.Auth, convertAPIAuthToCRD(info.Auth))
}

func TestConversionOfAnAuthWithoutSigV4(t *testing.T) {
	adapter := &Adapter{namespace: "muster"}

	crd := adapter.convertRequestToCRD(&api.MCPServerCreateRequest{
		Name: "plain",
		Type: string(api.MCPServerTypeStreamableHTTP),
		URL:  "https://example.com/mcp",
		Auth: &api.MCPServerAuth{Type: "none"},
	})

	assert.Nil(t, crd.Spec.Auth.SigV4)
	assert.Nil(t, crd.Spec.Meta)
	assert.Nil(t, convertCRDToInfo(crd).Auth.SigV4)
}
