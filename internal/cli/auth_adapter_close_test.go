package cli

import (
	"testing"

	"github.com/giantswarm/muster/internal/api"
)

// TestAuthAdapter_Close_Unregisters pins the symmetry Register() and Close()
// need in order for this shape to be safe:
//
//	defer func() { _ = adapter.Close() }()
//	adapter.Register()
//
// Register() publishes the adapter into a process-global registry; Close() tears
// down its managers but never unpublishes. The global keeps pointing at a closed
// adapter, so a later api.GetAuthHandler() hands out a husk. Because AuthAdapter
// carries no closed flag, the husk silently re-creates managers on next use
// rather than failing loudly.
//
// Register and Close must be symmetric: closing an adapter that is the currently
// registered handler has to clear the registration.
func TestAuthAdapter_Close_Unregisters(t *testing.T) {
	adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
		TokenStorageDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	prev := api.SwapAuthHandler(nil)
	t.Cleanup(func() { api.SwapAuthHandler(prev) })

	adapter.Register()
	if api.GetAuthHandler() == nil {
		t.Fatal("precondition failed: adapter should be registered after Register()")
	}

	if err := adapter.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if got := api.GetAuthHandler(); got != nil {
		t.Errorf("after Close() the registry still returns %T; a closed adapter must not stay reachable via api.GetAuthHandler()", got)
	}
}

// TestAuthAdapter_Close_LeavesOtherHandlerRegistered guards the fix from
// overreaching: closing an adapter that is *not* the registered handler must
// leave the registry alone.
func TestAuthAdapter_Close_LeavesOtherHandlerRegistered(t *testing.T) {
	registered, err := NewAuthAdapterWithConfig(AuthAdapterConfig{TokenStorageDir: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create registered adapter: %v", err)
	}
	other, err := NewAuthAdapterWithConfig(AuthAdapterConfig{TokenStorageDir: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create second adapter: %v", err)
	}

	prev := api.SwapAuthHandler(nil)
	t.Cleanup(func() { api.SwapAuthHandler(prev) })

	registered.Register()
	t.Cleanup(func() { _ = registered.Close() })

	if err := other.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if got := api.GetAuthHandler(); got != api.AuthHandler(registered) {
		t.Errorf("closing an unregistered adapter changed the registry to %v, want the still-registered adapter", got)
	}
}
