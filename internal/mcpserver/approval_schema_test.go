package mcpserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// pruningClient is an API server whose MCPServer CRD predates approval
// settings: it accepts every write and drops spec.approval.
type pruningClient struct {
	recordingClient
	knows bool
}

func (p *pruningClient) MCPServerSpecHasField(context.Context, string) (bool, error) {
	return p.knows, nil
}

func (p *pruningClient) CreateMCPServer(ctx context.Context, obj *musterv1alpha1.MCPServer) error {
	pruned := obj.DeepCopy()
	pruned.Spec.Approval = nil
	return p.recordingClient.CreateMCPServer(ctx, pruned)
}

func (p *pruningClient) UpdateMCPServer(ctx context.Context, obj *musterv1alpha1.MCPServer) error {
	pruned := obj.DeepCopy()
	pruned.Spec.Approval = nil
	return p.recordingClient.UpdateMCPServer(ctx, pruned)
}

// A CRD without spec.approval is detected: the write tools refuse to store
// settings instead of reporting success.
func TestApprovalSchema_StaleCRDRefusesWrites(t *testing.T) {
	c := &pruningClient{}
	c.existing = &musterv1alpha1.MCPServer{}
	c.existing.Name = "github"
	adapter := NewAdapterWithClient(c, "test-ns")

	text, isErr := call(t, adapter, "mcpserver_set_approval", map[string]interface{}{"name": "github", "approval": map[string]interface{}{"rule": "Ask"}})
	require.True(t, isErr)
	assert.Contains(t, text, "does not know spec.approval")
	assert.Empty(t, c.updatedObjs, "nothing is written")

	text, isErr = call(t, adapter, "mcpserver_create", remoteServerArgs(map[string]interface{}{"preset": "ask-all"}))
	require.True(t, isErr)
	assert.Contains(t, text, "does not know spec.approval")
	assert.Empty(t, c.createdObjs)
}

// Even when the check says the CRD knows the field, a write the API server
// stored without the settings is read back and reported as not stored.
func TestApprovalSchema_ReadBackCatchesPrunedWrite(t *testing.T) {
	c := &pruningClient{knows: true}
	c.existing = &musterv1alpha1.MCPServer{}
	c.existing.Name = "github"
	adapter := NewAdapterWithClient(c, "test-ns")

	text, isErr := call(t, adapter, "mcpserver_set_approval", map[string]interface{}{"name": "github", "approval": map[string]interface{}{"rule": "Ask"}})
	require.True(t, isErr)
	assert.Contains(t, text, "were not stored")

	text, isErr = call(t, adapter, "mcpserver_create", remoteServerArgs(map[string]interface{}{"preset": "ask-all"}))
	require.True(t, isErr)
	assert.Contains(t, text, "were not stored")
}

func TestApprovalSchema_ProblemIsRechecked(t *testing.T) {
	c := &pruningClient{}
	adapter := NewAdapterWithClient(c, "test-ns")
	ctx := context.Background()
	assert.Contains(t, adapter.ApprovalSchemaProblem(ctx), "does not know spec.approval")
	c.knows = true
	adapter.approvalSchema.forget()
	assert.Empty(t, adapter.ApprovalSchemaProblem(ctx), "an upgraded CRD is picked up")

	filesystem := &stubMusterClient{filesystem: true}
	assert.Empty(t, NewAdapterWithClient(filesystem, "test-ns").ApprovalSchemaProblem(ctx), "filesystem mode stores the field as written")
}
