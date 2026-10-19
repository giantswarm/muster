package mcpserver

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/callerwrite"
	"github.com/giantswarm/muster/v5/internal/server"
)

// listingFactory is a caller client factory whose every list in a
// namespace answers with err (nil: allowed), and counts the lists.
type listingFactory struct {
	err   atomic.Pointer[error]
	lists atomic.Int32
}

func (f *listingFactory) factory(string) (ctrlclient.Client, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(musterv1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(context.Context, ctrlclient.WithWatch, ctrlclient.ObjectList, ...ctrlclient.ListOption) error {
			f.lists.Add(1)
			if err := f.err.Load(); err != nil {
				return *err
			}
			return nil
		},
	}).Build(), nil
}

// The answer for one token is reused for a few seconds, so a listing of every
// server costs no round trip per server; an answer muster could not read
// (an API error, a timeout) hides the namespace for that request only and is
// not reused; a token without the API server's audience never reaches the
// API server.
func TestVisibility_CachesOnlyCompleteAnswers(t *testing.T) {
	lists := &listingFactory{}
	v := NewVisibility(nil, lists.factory, "", "muster")
	now := time.Unix(0, 0)
	v.now = func() time.Time { return now }
	ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))

	transient := errors.New("connection reset")
	lists.err.Store(&transient)
	if v.CallerVisibility(ctx).Sees("github") {
		t.Fatal("a namespace whose list failed is hidden")
	}
	lists.err.Store(nil)
	if !v.CallerVisibility(ctx).Sees("github") {
		t.Fatal("the failed answer was reused; the next request must ask again")
	}
	before := lists.lists.Load()
	v.CallerVisibility(ctx)
	if lists.lists.Load() != before {
		t.Errorf("a complete answer within the TTL is reused, got %d more lists", lists.lists.Load()-before)
	}
	now = now.Add(visibilityTTL)
	v.CallerVisibility(ctx)
	if lists.lists.Load() == before {
		t.Error("after the TTL the caller's permissions are read again")
	}

	before = lists.lists.Load()
	noAudience := server.ContextWithIDToken(context.Background(), testJWT(t, "muster"))
	if view := v.CallerVisibility(noAudience); view.Sees("github") || !strings.Contains(view.Reason, "sign in again") {
		t.Errorf("a token without the audience: %+v, want no server and a re-login reason", view)
	}
	if lists.lists.Load() != before {
		t.Error("a token without the audience is sent to the API server")
	}
}
