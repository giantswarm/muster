package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

var (
	testRequestMeta    = map[string]string{api.AWSRegionRequestMetaKey: "eu-central-1"}
	testDeprecatedMeta = map[string]string{api.AWSRegionRequestMetaKey: "us-east-1"}
)

func TestCRDRequestMeta(t *testing.T) {
	assert.Equal(t, testRequestMeta, crdRequestMeta(musterv1alpha1.MCPServerSpec{RequestMeta: testRequestMeta}))
	assert.Equal(t, testDeprecatedMeta, crdRequestMeta(musterv1alpha1.MCPServerSpec{Meta: testDeprecatedMeta})) //nolint:staticcheck // SA1019: covers the deprecated name
	assert.Nil(t, crdRequestMeta(musterv1alpha1.MCPServerSpec{}))
	assert.Equal(t, testRequestMeta, crdRequestMeta(musterv1alpha1.MCPServerSpec{RequestMeta: testRequestMeta, Meta: testDeprecatedMeta})) //nolint:staticcheck // SA1019: covers the deprecated name
}

func TestCreateWritesTheDeprecatedNameAsRequestMeta(t *testing.T) {
	adapter := &Adapter{namespace: "muster"}

	crd := adapter.convertRequestToCRD(&api.MCPServerCreateRequest{
		Name: "aws",
		Type: string(api.MCPServerTypeStreamableHTTP),
		URL:  testSigV4URL,
		Meta: testDeprecatedMeta,
	})

	assert.Equal(t, testDeprecatedMeta, crd.Spec.RequestMeta)
	assert.Nil(t, crd.Spec.Meta) //nolint:staticcheck // SA1019: covers the deprecated name
}

func TestValidateRefusesBothNames(t *testing.T) {
	adapter := &Adapter{namespace: "muster"}

	crd := adapter.convertRequestToCRD(&api.MCPServerCreateRequest{
		Name:        "aws",
		Type:        string(api.MCPServerTypeStreamableHTTP),
		URL:         testSigV4URL,
		RequestMeta: testRequestMeta,
		Meta:        testDeprecatedMeta,
	})

	require.ErrorContains(t, adapter.validateMCPServer(crd), "set requestMeta only")
}

func TestValidateRefusesSigV4WithoutRegion(t *testing.T) {
	adapter := &Adapter{namespace: "muster"}

	crd := adapter.convertRequestToCRD(&api.MCPServerCreateRequest{
		Name: "aws",
		Type: string(api.MCPServerTypeStreamableHTTP),
		URL:  testSigV4URL,
		Auth: &api.MCPServerAuth{
			Type:  api.MCPServerAuthTypeSigV4,
			SigV4: &api.MCPServerSigV4{Region: testSigV4Region},
		},
	})

	require.ErrorContains(t, adapter.validateMCPServer(crd), "needs requestMeta.AWS_REGION")
}

func TestUpdateWritesRequestMeta(t *testing.T) {
	tests := []struct {
		name            string
		existing        musterv1alpha1.MCPServerSpec
		req             api.MCPServerUpdateRequest
		wantRequestMeta map[string]string
		wantMeta        map[string]string
	}{
		{
			name:            "requestMeta replaces the deprecated field",
			existing:        musterv1alpha1.MCPServerSpec{Meta: testDeprecatedMeta}, //nolint:staticcheck // SA1019: covers the deprecated name
			req:             api.MCPServerUpdateRequest{RequestMeta: testRequestMeta},
			wantRequestMeta: testRequestMeta,
		},
		{
			name:            "the deprecated name is written as requestMeta",
			existing:        musterv1alpha1.MCPServerSpec{RequestMeta: testRequestMeta},
			req:             api.MCPServerUpdateRequest{Meta: testDeprecatedMeta},
			wantRequestMeta: testDeprecatedMeta,
		},
		{
			name:            "an update without either keeps the stored entries",
			existing:        musterv1alpha1.MCPServerSpec{RequestMeta: testRequestMeta},
			wantRequestMeta: testRequestMeta,
		},
		{
			name:            "both names are kept so validation refuses them",
			existing:        musterv1alpha1.MCPServerSpec{},
			req:             api.MCPServerUpdateRequest{RequestMeta: testRequestMeta, Meta: testDeprecatedMeta},
			wantRequestMeta: testRequestMeta,
			wantMeta:        testDeprecatedMeta,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &Adapter{namespace: "muster"}
			existing := &musterv1alpha1.MCPServer{Spec: tt.existing}
			existing.Name = "aws"
			existing.Spec.Type = string(api.MCPServerTypeStreamableHTTP)
			existing.Spec.URL = testSigV4URL

			refused := adapter.applyMCPServerUpdate(existing, &tt.req)

			assert.Equal(t, tt.wantRequestMeta, existing.Spec.RequestMeta)
			assert.Equal(t, tt.wantMeta, existing.Spec.Meta) //nolint:staticcheck // SA1019: covers the deprecated name
			if tt.wantMeta != nil {
				require.NotNil(t, refused)
				require.True(t, refused.IsError)
				return
			}
			require.Nil(t, refused)
		})
	}
}
