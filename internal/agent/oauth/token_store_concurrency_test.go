package oauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// newFileStore opens a file-backed store on dir, the way a muster process does.
func newFileStore(t *testing.T, dir string) *TokenStore {
	t.Helper()
	store, err := NewTokenStore(TokenStoreConfig{StorageDir: dir, FileMode: true})
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
	return store
}

func numberedToken(prefix string, i int) *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  fmt.Sprintf("%s-%d", prefix, i),
		RefreshToken: "refresh-" + prefix,
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}
}

// Two processes -- two stores on one directory, each with its own cache --
// refresh different endpoints at the same time. Both files survive with the
// last token each process wrote, and no temporary file is left behind.
func TestTokenStore_ConcurrentProcessesDifferentEndpoints(t *testing.T) {
	dir := t.TempDir()
	const rounds = 200
	endpoints := map[string]string{
		"https://muster.gazelle.example/mcp": "gazelle",
		"https://muster.glean.example/mcp":   "glean",
	}
	stores := make(map[string]*TokenStore, len(endpoints))
	for endpoint := range endpoints {
		stores[endpoint] = newFileStore(t, dir)
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(endpoints)*rounds)
	for endpoint, name := range endpoints {
		wg.Add(1)
		go func(store *TokenStore, endpoint, name string) {
			defer wg.Done()
			for i := 1; i <= rounds; i++ {
				if err := store.StoreToken(endpoint, testDexURL, numberedToken(name, i)); err != nil {
					errs <- err
				}
			}
		}(stores[endpoint], endpoint, name)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("StoreToken: %v", err)
	}

	// A third process reads what the two left behind.
	reader := newFileStore(t, dir)
	for endpoint, name := range endpoints {
		got := reader.GetToken(endpoint)
		if got == nil {
			t.Fatalf("token for %s vanished", endpoint)
		}
		if want := fmt.Sprintf("%s-%d", name, rounds); got.AccessToken != want {
			t.Errorf("%s: access token %q, want %q", endpoint, got.AccessToken, want)
		}
	}
	assertOnlyTokenFiles(t, dir, len(endpoints))
}

// One process refreshes an endpoint over and over while another reads the
// same file. Every read parses: the reader sees the old or the new token,
// never a truncated file -- which the store would report as "no token" and
// the CLI would answer with a sign-in.
func TestTokenStore_RewriteNeverExposesPartialFile(t *testing.T) {
	dir := t.TempDir()
	writer := newFileStore(t, dir)
	reader := newFileStore(t, dir)
	const endpoint = "https://muster.gazelle.example/mcp"
	if err := writer.StoreToken(endpoint, testDexURL, numberedToken("gazelle", 0)); err != nil {
		t.Fatalf("StoreToken: %v", err)
	}
	key := reader.tokenKey(endpoint)

	written := make(chan error, 1)
	go func() {
		defer close(written)
		for i := 1; i <= 500; i++ {
			if err := writer.StoreToken(endpoint, testDexURL, numberedToken("gazelle", i)); err != nil {
				written <- err
				return
			}
		}
	}()

	reads := 0
	for done := false; !done; {
		select {
		case err, more := <-written:
			if more {
				t.Fatalf("StoreToken: %v", err)
			}
			done = true
		default:
		}
		token, err := reader.readTokenFile(key)
		if err != nil {
			t.Fatalf("read %d saw a partial or missing file: %v", reads, err)
		}
		if !strings.HasPrefix(token.AccessToken, "gazelle-") {
			t.Fatalf("read %d: access token %q", reads, token.AccessToken)
		}
		reads++
	}
	if reads == 0 {
		t.Fatal("no read happened")
	}
	assertOnlyTokenFiles(t, dir, 1)
}

// assertOnlyTokenFiles checks the store holds exactly want token files,
// each 0600, and nothing else (no temporary file of an atomic write).
func assertOnlyTokenFiles(t *testing.T, dir string, want int) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(entries) != want {
		t.Fatalf("%d entries in the store, want %d token files: %v", len(entries), want, names)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			t.Errorf("leftover %s in the store", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s has mode %o, want 0600", entry.Name(), perm)
		}
	}
}

// Every write and removal names the process behind it -- pid and command
// words -- and never a token value.
func TestTokenStore_ChangesAreLoggedWithPidAndCommand(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	store := newFileStore(t, t.TempDir())
	if err := store.StoreToken(testMusterURL, testDexURL, numberedToken("a", 1)); err != nil {
		t.Fatalf("StoreToken: %v", err)
	}
	if err := store.DeleteToken(testMusterURL); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	if err := store.StoreToken(testMusterURL, testDexURL, numberedToken("a", 2)); err != nil {
		t.Fatalf("StoreToken: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	events := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		event, _ := record["event"].(string)
		if event == "" {
			continue
		}
		events[event] = true
		if pid, ok := record["pid"].(float64); !ok || int(pid) != os.Getpid() {
			t.Errorf("%s: pid %v, want %d", event, record["pid"], os.Getpid())
		}
		if command, _ := record["command"].(string); command == "" {
			t.Errorf("%s: no command", event)
		}
		for _, secret := range []string{"a-1", "a-2", "refresh-a"} {
			if strings.Contains(line, secret) {
				t.Errorf("%s logged a token value: %s", event, line)
			}
		}
	}
	for _, want := range []string{"token_stored", "token_deleted", "token_file_removed", "tokens_cleared"} {
		if !events[want] {
			t.Errorf("no %s log line; events seen: %v", want, events)
		}
	}
}

func TestCommandWords(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"/usr/local/bin/muster"}, "muster"},
		{[]string{"muster", "auth", "logout", "--all"}, "muster auth logout"},
		{[]string{"muster", "call", "x_model-manager_unload_model", "--context", "gazelle", "--name=llama"}, "muster call x_model-manager_unload_model"},
		{[]string{"muster", "--debug", "call", "x"}, "muster"},
	}
	for _, tt := range tests {
		if got := commandWords(tt.args); got != tt.want {
			t.Errorf("commandWords(%v) = %q, want %q", tt.args, got, tt.want)
		}
	}
}
