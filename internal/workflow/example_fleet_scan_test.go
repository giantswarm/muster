package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/jq"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// fakeKubernetes answers x_kubernetes_list like mcp-kubernetes with
// fullOutput: mc-a has workload clusters a1 and a2 (a2 unreachable), mc-b's
// cluster listing fails, and a1's pods span two pages.
func fakeKubernetes(_ string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	pod := func(name, image string) map[string]interface{} {
		return map[string]interface{}{
			"kind":     "Pod",
			"metadata": map[string]interface{}{"name": name, "namespace": "shop", "annotations": map[string]interface{}{"blob": "FULL-MANIFEST"}},
			"spec": map[string]interface{}{
				"initContainers": []interface{}{map[string]interface{}{"name": "init", "image": "busybox:1"}},
				"containers":     []interface{}{map[string]interface{}{"name": "main", "image": image}},
			},
		}
	}
	page := func(items []interface{}, token string) (*mcp.CallToolResult, error) {
		data, err := json.Marshal(map[string]interface{}{"items": items, "continue": token})
		if err != nil {
			return nil, err
		}
		return textResult(string(data)), nil
	}
	errorResult := func(msg string) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(msg)}, IsError: true}, nil
	}

	mc, cluster, kind := args["management_cluster"], args["cluster"], args["resourceType"]
	switch {
	case kind == "clusters" && mc == "mc-a":
		return page([]interface{}{
			map[string]interface{}{"metadata": map[string]interface{}{"name": "a1", "namespace": "org-a"}},
			map[string]interface{}{"metadata": map[string]interface{}{"name": "a2", "namespace": "org-a"}},
		}, "")
	case kind == "clusters":
		return errorResult("management cluster mc-b: forbidden")
	case cluster == "a2":
		return errorResult("workload cluster a2 is unreachable")
	case kind == "pods" && args["continue"] == "p2":
		return page([]interface{}{pod("web-2", "registry.example.com/nginx:1.27")}, "")
	case kind == "pods":
		return page([]interface{}{pod("web-1", "registry.example.com/nginx:1.25"), pod("db-1", "postgres:16")}, "p2")
	case kind == "cronjobs":
		return page([]interface{}{map[string]interface{}{
			"kind":     "CronJob",
			"metadata": map[string]interface{}{"name": "report", "namespace": "batch"},
			"spec": map[string]interface{}{"jobTemplate": map[string]interface{}{"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
				"containers": []interface{}{map[string]interface{}{"name": "report", "image": "nginx:1.25-alpine"}},
			}}}}},
		}}, "")
	default:
		return page([]interface{}{}, "")
	}
}

// TestExampleFleetImageScan runs examples/workflow-fleet-image-scan.yaml: it
// must pass the validation a Workflow resource goes through and report the
// matches and the clusters it could not scan.
func TestExampleFleetImageScan(t *testing.T) {
	data, err := os.ReadFile("../../examples/workflow-fleet-image-scan.yaml")
	require.NoError(t, err)
	var crd musterv1alpha1.Workflow
	require.NoError(t, yaml.Unmarshal(data, &crd))
	wf := (&Adapter{}).convertCRDToWorkflow(&crd)
	require.NoError(t, api.ValidateLoopsPagingAndJQ(wf, jq.Check))

	executor := NewWorkflowExecutor(&scriptedToolCaller{responder: fakeKubernetes}, nil)
	result, err := executor.ExecuteWorkflow(context.Background(), wf, map[string]interface{}{
		"management_clusters": []interface{}{"mc-a", "mc-b"},
		"image":               "nginx",
	})
	require.NoError(t, err)
	doc := decodeDoc(t, result)

	assert.EqualValues(t, 2, doc["clusters_scanned"])
	var rows []string
	for _, m := range doc["matches"].([]interface{}) {
		row := m.(map[string]interface{})
		rows = append(rows, fmt.Sprintf("%s/%s %s %s/%s", row["management_cluster"], row["cluster"], row["kind"], row["namespace"], row["name"]))
	}
	// Rows come per cluster, kinds in the order of their step ids.
	assert.Equal(t, []string{
		"mc-a/a1 CronJob batch/report",
		"mc-a/a1 Pod shop/web-1",
		"mc-a/a1 Pod shop/web-2",
	}, rows)

	failures := doc["failures"].([]interface{})
	require.Len(t, failures, 2)
	assert.Equal(t, "mc-b", failures[0].(map[string]interface{})["management_cluster"])
	assert.Contains(t, failures[0].(map[string]interface{})["error"], "forbidden")
	assert.Equal(t, "a2", failures[1].(map[string]interface{})["cluster"])
	assert.Contains(t, failures[1].(map[string]interface{})["error"], "unreachable")

	text := result.Content[0].(mcp.TextContent).Text
	assert.NotContains(t, text, "FULL-MANIFEST")
	assert.NotContains(t, text, "db-1")
}
