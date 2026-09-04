package client

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// fakeAPIServer stands in for an apiserver during client construction. The
// only request the Kubernetes client makes while it is built is the CRD
// discovery GET for muster's API group; the first failFirst requests are
// answered 503, as an apiserver that is still starting would, and every later
// one with a resource list that carries the MCPServer kind.
type fakeAPIServer struct {
	*httptest.Server
	requests  atomic.Int32
	failFirst int32
}

func newFakeAPIServer(t *testing.T, failFirst int32) *fakeAPIServer {
	t.Helper()
	s := &fakeAPIServer{failFirst: failFirst}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.requests.Add(1) <= s.failFirst {
			http.Error(w, "apiserver not ready", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path != "/apis/muster.giantswarm.io/v1alpha1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metav1.APIResourceList{
			TypeMeta:     metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"},
			GroupVersion: "muster.giantswarm.io/v1alpha1",
			APIResources: []metav1.APIResource{{
				Name:         "mcpservers",
				SingularName: "mcpserver",
				Namespaced:   true,
				Kind:         "MCPServer",
				Verbs:        []string{"get", "list", "watch"},
			}},
		})
	}))
	t.Cleanup(s.Close)
	return s
}

// pointKubeconfigAt makes controller-runtime's config detection resolve to
// server for the duration of the test.
func pointKubeconfigAt(t *testing.T, server string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: test
`, server)
	require.NoError(t, os.WriteFile(path, []byte(kubeconfig), 0o600))
	t.Setenv("KUBECONFIG", path)
}

// fastBackoff replaces the production retry schedule with steps attempts a
// millisecond apart.
func fastBackoff(t *testing.T, steps int) {
	t.Helper()
	setBackoff(t, wait.Backoff{Duration: time.Millisecond, Factor: 1, Steps: steps})
}

func setBackoff(t *testing.T, backoff wait.Backoff) {
	t.Helper()
	orig := KubernetesClientBackoff
	KubernetesClientBackoff = backoff
	t.Cleanup(func() { KubernetesClientBackoff = orig })
}

// The production schedule must make exactly the number of attempts it
// documents. wait.Backoff.Step zeroes the remaining steps once the sleep
// reaches Cap, so a capped schedule attempts fewer times than Steps says —
// the first lab run of this fix logged "attempt 4/5" and gave up. The shape
// is exercised with its durations scaled from seconds to microseconds.
func TestKubernetesClientBackoff_MakesEveryDocumentedAttempt(t *testing.T) {
	scaled := KubernetesClientBackoff
	scaled.Duration /= 1_000_000
	scaled.Cap /= 1_000_000
	setBackoff(t, scaled)

	server := newFakeAPIServer(t, math.MaxInt32)
	pointKubeconfigAt(t, server.URL)

	_, err := NewMusterClientWithConfig(&MusterClientConfig{
		FilesystemPath:        t.TempDir(),
		RequireKubernetesMode: true,
	})

	require.Error(t, err)
	assert.EqualValues(t, scaled.Steps, server.requests.Load())
	assert.Contains(t, err.Error(), fmt.Sprintf("after %d attempts", scaled.Steps))
}

// Regression test for issue #1143: with kubernetes mode required, an
// apiserver that never answers must produce an error, not a filesystem client
// that would reconcile every existing CR as deleted.
func TestNewMusterClientWithConfig_RequiredKubernetesModeDoesNotFallBack(t *testing.T) {
	server := newFakeAPIServer(t, math.MaxInt32)
	pointKubeconfigAt(t, server.URL)
	fastBackoff(t, 3)

	c, err := NewMusterClientWithConfig(&MusterClientConfig{
		FilesystemPath:        t.TempDir(),
		RequireKubernetesMode: true,
	})

	require.Error(t, err)
	assert.Nil(t, c)
	assert.Contains(t, err.Error(), "kubernetes mode is configured")
	assert.Contains(t, err.Error(), "refusing to fall back to filesystem mode")
	assert.Contains(t, err.Error(), "after 3 attempts")
	assert.EqualValues(t, 3, server.requests.Load(), "one discovery request per attempt, every attempt made")
}

func TestNewMusterClientWithConfig_RequiredKubernetesModeWaitsOutAStartingAPIServer(t *testing.T) {
	server := newFakeAPIServer(t, 2)
	pointKubeconfigAt(t, server.URL)
	fastBackoff(t, 5)

	c, err := NewMusterClientWithConfig(&MusterClientConfig{
		FilesystemPath:        t.TempDir(),
		RequireKubernetesMode: true,
	})

	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	assert.True(t, c.IsKubernetesMode())
	assert.EqualValues(t, 3, server.requests.Load(), "two failed attempts, then the one that succeeded")
}

func TestNewMusterClientWithConfig_AutoDetectionFallsBackToFilesystemWithoutRetry(t *testing.T) {
	server := newFakeAPIServer(t, math.MaxInt32)
	pointKubeconfigAt(t, server.URL)
	fastBackoff(t, 5)

	c, err := NewMusterClientWithConfig(&MusterClientConfig{FilesystemPath: t.TempDir()})

	require.NoError(t, err)
	assert.False(t, c.IsKubernetesMode())
	assert.EqualValues(t, 1, server.requests.Load(), "automatic detection tries once")
}

func TestNewMusterClientWithConfig_ForceFilesystemModeNeverContactsTheAPIServer(t *testing.T) {
	server := newFakeAPIServer(t, 0)
	pointKubeconfigAt(t, server.URL)

	c, err := NewMusterClientWithConfig(&MusterClientConfig{
		FilesystemPath:      t.TempDir(),
		ForceFilesystemMode: true,
	})

	require.NoError(t, err)
	assert.False(t, c.IsKubernetesMode())
	assert.Zero(t, server.requests.Load())
}

func TestNewMusterClientWithConfig_ForceAndRequireAreMutuallyExclusive(t *testing.T) {
	c, err := NewMusterClientWithConfig(&MusterClientConfig{
		ForceFilesystemMode:   true,
		RequireKubernetesMode: true,
	})

	require.Error(t, err)
	assert.Nil(t, c)
}
