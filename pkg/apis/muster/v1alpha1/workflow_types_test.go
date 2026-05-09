package v1alpha1

import (
	"encoding/json"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// TestWorkflowStepArgs_ScalarsRoundTrip asserts that a Workflow whose step
// args contain scalar JSON values (string, integer, boolean) decodes
// without losing types and re-encodes to the same wire form.
//
// Regression coverage for the CRD schema bug where step args used to be
// declared as map[string]*runtime.RawExtension, which controller-tools
// emits with `additionalProperties: {type: object,
// x-kubernetes-preserve-unknown-fields: true}` -- forcing every value to
// be a JSON object and rejecting scalars at API-server validation time.
//
// The fix swapped the field type to map[string]apiextensionsv1.JSON,
// which controller-tools emits with bare
// `additionalProperties: {x-kubernetes-preserve-unknown-fields: true}`,
// so scalars validate.
func TestWorkflowStepArgs_ScalarsRoundTrip(t *testing.T) {
	const src = `
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: scalar-args-roundtrip
  namespace: default
spec:
  description: "Asserts scalar-valued step args round-trip correctly."
  steps:
    - id: list_pods
      tool: kubernetes_list
      args:
        resourceType: pods
        namespace: kube-system
        allNamespaces: false
        limit: 30
        fieldSelector: "status.phase!=Running"
`

	var wf Workflow
	if err := yaml.Unmarshal([]byte(src), &wf); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	if got, want := len(wf.Spec.Steps), 1; got != want {
		t.Fatalf("steps: got %d want %d", got, want)
	}
	args := wf.Spec.Steps[0].Args
	if args == nil {
		t.Fatalf("step args nil")
	}

	cases := map[string]string{
		"resourceType":  `"pods"`,
		"namespace":     `"kube-system"`,
		"allNamespaces": `false`,
		"limit":         `30`,
		"fieldSelector": `"status.phase!=Running"`,
	}
	for key, wantRaw := range cases {
		v, ok := args[key]
		if !ok {
			t.Errorf("args[%q] missing", key)
			continue
		}
		if string(v.Raw) != wantRaw {
			t.Errorf("args[%q].Raw = %s, want %s", key, string(v.Raw), wantRaw)
		}
	}

	// Round-trip back to JSON and ensure no panics / drops.
	out, err := json.Marshal(wf.Spec.Steps[0].Args)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var decoded map[string]apiextensionsv1.JSON
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	if got, want := len(decoded), len(cases); got != want {
		t.Fatalf("round-trip arg count: got %d want %d", got, want)
	}
}
