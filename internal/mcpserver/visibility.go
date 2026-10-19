package mcpserver

import (
	"context"
	"crypto/sha256"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
	"github.com/giantswarm/muster/v5/pkg/logging"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/callerwrite"
	"github.com/giantswarm/muster/v5/internal/client"
)

// visibilityTTL is how long the answer for one caller token is reused. A
// listing reads every server of every session, so the answer is cached; a
// RoleBinding that is added or removed takes effect within this time.
const visibilityTTL = 5 * time.Second

// visibilityListTimeout bounds one round of lists for one caller.
const visibilityListTimeout = 5 * time.Second

// visibilityListConcurrency is how many namespaces are listed at a time.
const visibilityListConcurrency = 8

// Visibility answers which MCP servers a caller may see (agent-permissions
// D9): muster lists MCPServer objects as the caller, with the caller's own ID
// token, once per namespace that holds servers, and the caller sees the
// servers of every namespace where the API server allows the list. muster's
// ServiceAccount gets no new right and muster keeps no copy of the claim
// mapping: RBAC answers.
type Visibility struct {
	servers         client.MusterClient
	factory         callerwrite.ClientFactory
	audience        string
	musterNamespace string

	mu    sync.Mutex
	cache map[[sha256.Size]byte]cachedVisibility
	now   func() time.Time

	// namespaces is the set of namespaces that hold servers, read as muster
	// at most once per visibilityTTL for every caller together.
	namespaces        []string
	namespacesExpires time.Time

	// flights coalesces the rounds of lists of one token, and the read of
	// the namespaces, across concurrent requests.
	flights singleflight.Group
}

type cachedVisibility struct {
	view    api.ServerVisibility
	expires time.Time
}

// NewVisibility returns the visibility handler. servers reads the MCPServer
// objects of every namespace as muster (to learn which namespaces hold
// servers); factory builds a client with the caller's token; audience is the
// audience the API server needs on that token (empty: the default).
func NewVisibility(servers client.MusterClient, factory callerwrite.ClientFactory, audience, musterNamespace string) *Visibility {
	if audience == "" {
		audience = callerwrite.DefaultKubernetesAudience
	}
	return &Visibility{
		servers:         servers,
		factory:         factory,
		audience:        audience,
		musterNamespace: musterNamespace,
		cache:           make(map[[sha256.Size]byte]cachedVisibility),
		now:             time.Now,
	}
}

// LoginAudience implements api.MCPServerVisibilityHandler.
func (v *Visibility) LoginAudience() string {
	return v.audience
}

// CallerVisibility implements api.MCPServerVisibilityHandler. It fails
// closed: a session without an ID token, with a token that lacks the API
// server's audience or that the API server rejects sees no server, not even
// the servers of muster's namespace, and is told to sign in again.
func (v *Visibility) CallerVisibility(ctx context.Context) api.ServerVisibility {
	token := callerwrite.TokenFromContext(ctx)
	if token == "" {
		return api.NoServerVisible(callerwrite.ReloginError("Your session carries no ID token, so muster cannot tell which MCP servers you may see."))
	}
	if callerwrite.TokenMissingAudience(token, v.audience) {
		return api.NoServerVisible(callerwrite.ReloginError(
			"Your session token does not carry the \"" + v.audience + "\" audience the Kubernetes API needs to tell which MCP servers you may see."))
	}

	key := sha256.Sum256([]byte(token))
	now := v.now()
	v.mu.Lock()
	if cached, ok := v.cache[key]; ok && now.Before(cached.expires) {
		v.mu.Unlock()
		return cached.view
	}
	v.mu.Unlock()

	// Concurrent requests of one token share one round of lists. The round
	// does not end with the request that started it: a client that hangs up
	// must not leave the others without an answer.
	result, _, _ := v.flights.Do(string(key[:]), func() (any, error) {
		flightCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), visibilityListTimeout)
		defer cancel()
		view, complete := v.listAsCaller(flightCtx, token)
		if complete {
			v.mu.Lock()
			for k, cached := range v.cache {
				if !now.Before(cached.expires) {
					delete(v.cache, k)
				}
			}
			v.cache[key] = cachedVisibility{view: view, expires: now.Add(visibilityTTL)}
			v.mu.Unlock()
		}
		return view, nil
	})
	return result.(api.ServerVisibility)
}

// listAsCaller lists the MCPServer objects of each namespace that holds
// servers with the caller's token, a few namespaces at a time. RBAC does not
// filter a list across namespaces, so there is one list per namespace.
// complete is false when an answer was neither a list nor an RBAC refusal (a
// timeout, an API error): the view then hides that namespace (fail closed)
// but is not cached, so the next request asks again.
func (v *Visibility) listAsCaller(ctx context.Context, token string) (view api.ServerVisibility, complete bool) {
	if v.factory == nil {
		return api.NoServerVisible("muster has no Kubernetes API access to tell which MCP servers you may see; check the muster deployment configuration."), true
	}
	caller, err := v.factory(token)
	if err != nil {
		logging.Warn("MCPServerVisibility", "Cannot build a client with the caller's token: %v", err)
		return api.NoServerVisible("muster cannot tell which MCP servers you may see: " + err.Error()), false
	}

	namespaces, complete := v.namespacesWithServers(ctx)
	allowed := make([]bool, len(namespaces))
	var unauthorized, transient atomic.Bool
	var wg sync.WaitGroup
	slots := make(chan struct{}, visibilityListConcurrency)
	for i, namespace := range namespaces {
		wg.Add(1)
		slots <- struct{}{}
		go func(i int, namespace string) {
			defer wg.Done()
			defer func() { <-slots }()
			err := caller.List(ctx, &musterv1alpha1.MCPServerList{}, ctrlclient.InNamespace(namespace), ctrlclient.Limit(1))
			switch {
			case err == nil:
				allowed[i] = true
			case apierrors.IsUnauthorized(err):
				unauthorized.Store(true)
			case apierrors.IsForbidden(err):
				// The caller may not list servers there: they stay hidden.
			default:
				// Fail closed for this namespace: an answer muster cannot
				// read is no permission, for this request only.
				transient.Store(true)
				logging.Warn("MCPServerVisibility", "Listing MCP servers in namespace %s as the caller failed: %v", namespace, err)
			}
		}(i, namespace)
	}
	wg.Wait()

	if unauthorized.Load() {
		return api.NoServerVisible(callerwrite.ReloginError("The Kubernetes API rejected your session token, so muster shows you no MCP server.")), true
	}
	var visible []string
	for i, namespace := range namespaces {
		if allowed[i] {
			visible = append(visible, namespace)
		}
	}
	return api.ServersOfNamespacesVisible(v.musterNamespace, visible), complete && !transient.Load()
}

// namespacesWithServers returns muster's namespace and every namespace that
// holds an MCPServer object muster serves, read as muster once per
// visibilityTTL for every caller together. complete is false when the read
// failed: the answer is then muster's namespace alone and is not cached.
func (v *Visibility) namespacesWithServers(ctx context.Context) (namespaces []string, complete bool) {
	now := v.now()
	v.mu.Lock()
	if v.namespaces != nil && now.Before(v.namespacesExpires) {
		namespaces := v.namespaces
		v.mu.Unlock()
		return namespaces, true
	}
	v.mu.Unlock()

	result, _, _ := v.flights.Do("namespaces", func() (any, error) {
		set := map[string]bool{v.musterNamespace: true}
		ok := true
		if v.servers != nil {
			servers, err := v.servers.ListMCPServers(ctx, "")
			if err != nil {
				ok = false
				logging.Warn("MCPServerVisibility", "Cannot list the namespaces that hold MCP servers: %v", err)
			}
			for i := range servers {
				if api.ServedNamespace(servers[i].Namespace, v.musterNamespace) {
					set[servers[i].Namespace] = true
				}
			}
		}
		read := make([]string, 0, len(set))
		for namespace := range set {
			read = append(read, namespace)
		}
		sort.Strings(read)
		if ok {
			v.mu.Lock()
			v.namespaces, v.namespacesExpires = read, now.Add(visibilityTTL)
			v.mu.Unlock()
		}
		return namespaceRead{namespaces: read, complete: ok}, nil
	})
	r := result.(namespaceRead)
	return r.namespaces, r.complete
}

type namespaceRead struct {
	namespaces []string
	complete   bool
}

var _ api.MCPServerVisibilityHandler = (*Visibility)(nil)
