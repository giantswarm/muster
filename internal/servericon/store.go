package servericon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// retryAfter is how long a src whose fetch failed transiently is not
// fetched again.
const retryAfter = 10 * time.Minute

type resolved struct {
	uri string
	err error
	at  time.Time
}

// Store holds each server's icon by server key (api.MCPServerKey) and
// resolves icons by src once.
type Store struct {
	mu       sync.Mutex
	icons    map[string]string
	srcs     map[string]string
	cache    map[string]resolved
	inflight map[string][]string
	onChange func(key string)
	fetcher  *fetcher
	now      func() time.Time
}

func newStore(f *fetcher) *Store {
	return &Store{
		icons:    map[string]string{},
		srcs:     map[string]string{},
		cache:    map[string]resolved{},
		inflight: map[string][]string{},
		fetcher:  f,
		now:      time.Now,
	}
}

var defaultStore = newStore(newFetcher(publicAddr, nil))

// Record takes the server info of a completed handshake; the icon is
// resolved in the background. Never blocks on the network.
func Record(key string, impl mcp.Implementation) { defaultStore.Record(key, impl) }

// Lookup returns the server's icon, "" when it sent no usable one; ok is
// false while muster has not learned it.
func Lookup(key string) (string, bool) { return defaultStore.Lookup(key) }

// SetOnChange sets the function told about a server whose icon changed.
func SetOnChange(fn func(key string)) { defaultStore.SetOnChange(fn) }

// Forget drops what the store knows of a server.
func Forget(key string) { defaultStore.Forget(key) }

func (s *Store) SetOnChange(fn func(key string)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

func (s *Store) Lookup(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	icon, ok := s.icons[key]
	return icon, ok
}

func (s *Store) Forget(key string) {
	s.mu.Lock()
	delete(s.icons, key)
	delete(s.srcs, key)
	s.mu.Unlock()
}

func (s *Store) Record(key string, impl mcp.Implementation) {
	icon, ok := Select(impl.Icons)
	src := icon.Src

	s.mu.Lock()
	if prev, known := s.srcs[key]; known && prev == src {
		if _, have := s.icons[key]; have {
			s.mu.Unlock()
			return
		}
	}
	s.srcs[key] = src
	if !ok {
		s.mu.Unlock()
		s.set(key, src, "")
		return
	}
	if r, cached := s.cache[src]; cached && (r.err == nil || errors.Is(r.err, errInvalid) || s.now().Sub(r.at) < retryAfter) {
		s.mu.Unlock()
		s.apply(key, src, r)
		return
	}
	if waiting, busy := s.inflight[src]; busy {
		s.inflight[src] = append(waiting, key)
		s.mu.Unlock()
		return
	}
	s.inflight[src] = []string{key}
	s.mu.Unlock()

	if !strings.HasPrefix(src, "https:") {
		s.resolve(src)
		return
	}
	go s.resolve(src)
}

func (s *Store) resolve(src string) {
	var uri string
	var err error
	if strings.HasPrefix(src, "data:") {
		uri, err = FromDataURI(src)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		uri, err = s.fetcher.fetch(ctx, src)
		cancel()
	}
	r := resolved{uri: uri, err: err, at: s.now()}

	s.mu.Lock()
	s.cache[src] = r
	keys := s.inflight[src]
	delete(s.inflight, src)
	s.mu.Unlock()

	for _, key := range keys {
		s.apply(key, src, r)
	}
}

// apply stores a resolved icon for a server. A transient failure keeps what
// the server had; an invalid icon is no icon.
func (s *Store) apply(key, src string, r resolved) {
	if r.err != nil {
		logging.Debug("ServerIcon", "Icon of %s not usable: %v", key, r.err)
		if !errors.Is(r.err, errInvalid) {
			return
		}
	}
	s.set(key, src, r.uri)
}

// set stores uri as the server's icon unless the server has since declared
// another src, and reports a change.
func (s *Store) set(key, src, uri string) {
	s.mu.Lock()
	if s.srcs[key] != src {
		s.mu.Unlock()
		return
	}
	prev := s.icons[key]
	s.icons[key] = uri
	onChange := s.onChange
	s.mu.Unlock()

	if prev != uri && onChange != nil {
		logging.Debug("ServerIcon", "Icon of %s changed", key)
		onChange(key)
	}
}
