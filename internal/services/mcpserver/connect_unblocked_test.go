package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/callerwrite"
	"github.com/giantswarm/muster/v5/internal/client"
	"github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/internal/server"
	"github.com/giantswarm/muster/v5/internal/services"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// servedObjects is the MCPServer objects muster serves, of every namespace.
type servedObjects struct {
	client.MusterClient
	servers []musterv1alpha1.MCPServer
}

func (c *servedObjects) IsKubernetesMode() bool { return true }

func (c *servedObjects) ListMCPServers(_ context.Context, namespace string) ([]musterv1alpha1.MCPServer, error) {
	var out []musterv1alpha1.MCPServer
	for _, s := range c.servers {
		if namespace == "" || s.Namespace == namespace {
			out = append(out, s)
		}
	}
	return out, nil
}

// callerJWT is an unsigned ID token of subject with the API server's audience;
// the API server, not muster, verifies it.
func callerJWT(t *testing.T, subject string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]any{"sub": subject, "iss": "https://dex.example.com", "aud": callerwrite.DefaultKubernetesAudience})
	require.NoError(t, err)
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// rbacFactory answers each caller's list like the API server: the token named
// rejected is Unauthorized, every other may list MCPServers in allowed
// namespaces and is Forbidden elsewhere.
func rbacFactory(rejected string, allowed ...string) callerwrite.ClientFactory {
	scheme := runtime.NewScheme()
	utilruntime.Must(musterv1alpha1.AddToScheme(scheme))
	may := map[string]bool{}
	for _, namespace := range allowed {
		may[namespace] = true
	}
	gr := schema.GroupResource{Group: musterv1alpha1.GroupVersion.Group, Resource: "mcpservers"}
	return func(token string) (ctrlclient.Client, error) {
		return fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, _ ctrlclient.WithWatch, _ ctrlclient.ObjectList, opts ...ctrlclient.ListOption) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if token == rejected {
					return apierrors.NewUnauthorized("token rejected")
				}
				var listOpts ctrlclient.ListOptions
				listOpts.ApplyOptions(opts)
				if !may[listOpts.Namespace] {
					return apierrors.NewForbidden(gr, "", fmt.Errorf("cannot list in %s", listOpts.Namespace))
				}
				return nil
			},
		}).Build(), nil
	}
}

// hangingConnect starts a service whose backend never answers initialize
// until the test ends, and returns once that connect is in flight, which
// holds the service's clientInitMutex. connectEnded closes when Start returns.
func hangingConnect(t *testing.T, name string) (svc *Service, connectEnded <-chan struct{}) {
	t.Helper()
	entered := make(chan struct{})
	release := make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enterOnce.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(backend.Close)

	svc, err := NewService(&api.MCPServer{
		Name:      name,
		Namespace: "muster",
		Type:      api.MCPServerTypeStreamableHTTP,
		URL:       backend.URL + "/mcp",
	})
	require.NoError(t, err)

	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = svc.Start(context.WithoutCancel(t.Context()))
	}()
	t.Cleanup(func() { unblock(); <-ended; _ = svc.Stop(context.Background()) })
	<-entered
	return svc, ended
}

// answeredWhileConnecting fails the test when answer has not returned within
// a bound, while the connect of the service is still in flight.
func answeredWhileConnecting(t *testing.T, connectEnded <-chan struct{}, what string, answer func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); answer() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s waited for a backend's connect", what)
	}
	select {
	case <-connectEnded:
		t.Fatal("the backend connect ended before the answer; the test no longer holds the connect lock")
	default:
	}
}

// Status readers answer while the service connects: the connect holds the
// client lock for its whole network round trip, and the service data, the
// client and its readiness are read without that lock.
func TestServiceReadersDoNotWaitForTheConnect(t *testing.T) {
	svc, connectEnded := hangingConnect(t, "hanging")

	var data map[string]any
	var client any
	var ready bool
	answeredWhileConnecting(t, connectEnded, "the service's readers", func() {
		data = svc.GetServiceData()
		client = svc.GetMCPClient()
		ready = svc.IsClientReady()
	})
	require.Equal(t, false, data["clientReady"])
	require.NotContains(t, data, "client")
	require.NotContains(t, data, api.ServiceDataProtocolVersion)
	require.Nil(t, client)
	require.False(t, ready)
}

// A backend whose connect hangs holds its client lock for the whole connect.
// Fresh callers must still learn which servers they see, from the served
// objects alone: they wait for neither that connect nor its lock, keep the
// RBAC answer of each namespace, and a rejected token still sees nothing.
func TestCallerVisibilityDoesNotWaitForABackendConnect(t *testing.T) {
	hanging, connectEnded := hangingConnect(t, "hanging")
	registry := services.NewRegistry()
	require.NoError(t, registry.Register(hanging))
	previous := api.GetServiceRegistry()
	api.RegisterServiceRegistry(services.NewRegistryAdapter(registry))
	t.Cleanup(func() { api.RegisterServiceRegistry(previous) })

	objects := &servedObjects{servers: []musterv1alpha1.MCPServer{
		{ObjectMeta: metav1.ObjectMeta{Name: "hanging", Namespace: "muster"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "team-a"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "billing", Namespace: "team-b"}},
	}}
	adapter := mcpserver.NewAdapterWithClient(objects, "muster")
	adapter.SetScope(mcpserver.Scope{Namespaces: labels.Everything()})
	rejected := callerJWT(t, "mallory")
	visibility := mcpserver.NewVisibility(adapter, rbacFactory(rejected, "muster", "team-a"), "", "muster")

	const callers = 8
	views := make([]api.ServerVisibility, callers+1)
	var wg sync.WaitGroup
	for i := range callers {
		ctx := server.ContextWithIDToken(t.Context(), callerJWT(t, fmt.Sprintf("caller-%d", i)))
		wg.Go(func() { views[i] = visibility.CallerVisibility(ctx) })
	}
	wg.Go(func() {
		views[callers] = visibility.CallerVisibility(server.ContextWithIDToken(t.Context(), rejected))
	})
	answeredWhileConnecting(t, connectEnded, "caller visibility", wg.Wait)

	for i := range callers {
		require.True(t, views[i].Sees("hanging"), "caller %d sees muster's namespace", i)
		require.True(t, views[i].Sees("team-a/github"), "caller %d may list team-a", i)
		require.False(t, views[i].Sees("team-b/billing"), "caller %d is forbidden in team-b", i)
	}
	denied := views[callers]
	require.False(t, denied.Sees("hanging"), "a rejected token sees no server")
	require.False(t, denied.Sees("team-a/github"), "a rejected token sees no server")
	require.Contains(t, denied.Reason, "sign in again")
}
