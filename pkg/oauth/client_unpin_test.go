package oauth

import (
	"context"
	"testing"
	"time"
)

func TestClient_UnpinMetadata(t *testing.T) {
	client := NewClient()
	pinned := "http://127.0.0.1:9/pinned-as" // nothing listens: a fetch fails at once
	client.PinMetadata(pinned, &Metadata{AuthorizationEndpoint: pinned + "/authorize", TokenEndpoint: pinned + "/access_token"})

	discovered := "https://dex.example.com"
	client.cacheMetadata(discovered, &Metadata{Issuer: discovered, AuthorizationEndpoint: discovered + "/auth", TokenEndpoint: discovered + "/token"})

	if !client.UnpinMetadata(pinned + "/") {
		t.Fatal("UnpinMetadata should report the pinned entry it dropped")
	}
	if client.UnpinMetadata(pinned) {
		t.Fatal("a second UnpinMetadata has nothing left to drop")
	}
	client.metadataMu.RLock()
	_, stillPinned := client.metadataCache[pinned]
	entry, kept := client.metadataCache[discovered]
	client.metadataMu.RUnlock()
	if stillPinned {
		t.Error("the pinned document must be gone so the next DiscoverMetadata fetches again")
	}
	if !kept || entry.pinned {
		t.Error("a discovered entry is left alone; it expires with the cache TTL")
	}

	// A discovered entry is not dropped by UnpinMetadata either.
	if client.UnpinMetadata(discovered) {
		t.Error("UnpinMetadata must not report a discovered entry as dropped")
	}

	// Without the pin, discovery for the issuer has to hit the network again.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := client.DiscoverMetadata(ctx, pinned); err == nil {
		t.Error("DiscoverMetadata should fetch (and fail without a server) once the pin is gone")
	}
}
