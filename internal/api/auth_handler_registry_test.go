package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// stubAuthHandler is an inert AuthHandler used to exercise the registry itself.
// Identity, not behavior, is what these tests assert on.
type stubAuthHandler struct{ id int }

func (s *stubAuthHandler) CheckAuthRequired(context.Context, string) (bool, error) { return false, nil }
func (s *stubAuthHandler) HasCredentials(string) bool                              { return false }
func (s *stubAuthHandler) GetBearerToken(string) (string, error)                   { return "", nil }
func (s *stubAuthHandler) Login(context.Context, string) error                     { return nil }
func (s *stubAuthHandler) LoginWithIssuer(context.Context, string, string) error   { return nil }
func (s *stubAuthHandler) Logout(string) error                                     { return nil }
func (s *stubAuthHandler) LogoutAll() error                                        { return nil }
func (s *stubAuthHandler) GetStatus() []AuthStatus                                 { return nil }
func (s *stubAuthHandler) GetStatusForEndpoint(string) *AuthStatus                 { return nil }
func (s *stubAuthHandler) InvalidateCache(string)                                  {}
func (s *stubAuthHandler) Close() error                                            { return nil }

// TestSwapAuthHandler_ReturnsPrevious pins the primitive tests need in order to
// isolate themselves from the process-global registry.
//
// Today the only way to restore the registry is RegisterAuthHandler(nil), which
// clobbers rather than restores: a test that resets to nil destroys whatever the
// enclosing test or package had registered. Five test files currently register
// handlers with no cleanup at all. A Swap that hands back the displaced value
// lets callers do t.Cleanup(func() { SwapAuthHandler(prev) }).
func TestSwapAuthHandler_ReturnsPrevious(t *testing.T) {
	first := &stubAuthHandler{id: 1}
	second := &stubAuthHandler{id: 2}

	prev := SwapAuthHandler(first)
	t.Cleanup(func() { SwapAuthHandler(prev) })

	if got := SwapAuthHandler(second); got != AuthHandler(first) {
		t.Fatalf("SwapAuthHandler returned %v, want the displaced handler %v", got, first)
	}
	if got := GetAuthHandler(); got != AuthHandler(second) {
		t.Fatalf("GetAuthHandler returned %v, want %v", got, second)
	}
}

// TestGetOrRegisterAuthHandler_ConstructsExactlyOnce is the regression test for
// the check-then-act that cmd.ensureAuthHandlerWithOptions and
// cli.ToolExecutor.setupAuthentication both used to perform.
//
// Each spelled get-or-create as three separate lock acquisitions: GetAuthHandler,
// then construct, then Register, then GetAuthHandler again. The RWMutex makes
// each step atomic but leaves the composite racy, so concurrent callers each
// build and publish their own adapter. Every loser is orphaned without Close(),
// and the final re-read of the global can hand a caller an adapter configured
// with someone else's options.
//
// The invariant: however many callers race, the factory runs exactly once and
// they all observe the same handler.
func TestGetOrRegisterAuthHandler_ConstructsExactlyOnce(t *testing.T) {
	prev := SwapAuthHandler(nil)
	t.Cleanup(func() { SwapAuthHandler(prev) })

	const goroutines = 64

	var constructed atomic.Int64
	results := make([]AuthHandler, goroutines)
	start := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			h, err := GetOrRegisterAuthHandler(func() (AuthHandler, error) {
				constructed.Add(1)
				return &stubAuthHandler{id: i}, nil
			})
			if err != nil {
				t.Errorf("goroutine %d: GetOrRegisterAuthHandler returned error: %v", i, err)
				return
			}
			results[i] = h
		}(i)
	}
	close(start)
	wg.Wait()

	if n := constructed.Load(); n != 1 {
		t.Errorf("factory ran %d times, want exactly 1: each racing caller built and registered its own handler, orphaning the losers without Close()", n)
	}
	for i, h := range results {
		if h != results[0] {
			t.Errorf("caller %d observed handler %p, want the single shared handler %p", i, h, results[0])
		}
	}
}
