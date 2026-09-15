package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

// newValkeyCapabilityStoreForTest runs the store against an in-process
// miniredis, so the layout on the wire — hash fields, documents, TTLs — is
// asserted on a real key space rather than on mocked commands.
func newValkeyCapabilityStoreForTest(t *testing.T, ttl time.Duration) (*ValkeyCapabilityStore, *miniredis.Miniredis) {
	t.Helper()
	srv := miniredis.RunT(t)
	return newValkeyCapabilityStoreOn(t, srv, ttl), srv
}

// newValkeyCapabilityStoreOn is a further store instance on the same key
// space -- another muster process, or the same one after a restart -- with
// its own empty document cache.
func newValkeyCapabilityStoreOn(t *testing.T, srv *miniredis.Miniredis, ttl time.Duration) *ValkeyCapabilityStore {
	t.Helper()
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{srv.Addr()},
		DisableCache: true,
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return NewValkeyCapabilityStore(client, ttl, "muster:")
}

// assertSameCapabilities compares through JSON: a document that went through
// the store loses the distinction between an empty and a nil slice inside the
// tool schemas, which is what the store's readers see anyway.
func assertSameCapabilities(t *testing.T, want, got *Capabilities) {
	t.Helper()
	require.NotNil(t, got)
	wantJSON, err := json.Marshal(want)
	require.NoError(t, err)
	gotJSON, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantJSON), string(gotJSON))
}

func bigCapabilities(marker string) *Capabilities {
	tools := make([]mcp.Tool, 0, 40)
	for i := 0; i < 40; i++ {
		tools = append(tools, mcp.Tool{
			Name:        marker + "-tool-" + strings.Repeat("x", i),
			Description: strings.Repeat("a tool that does things ", 20),
		})
	}
	return &Capabilities{
		Tools:     tools,
		Resources: []mcp.Resource{{Name: marker + "-res"}},
		Prompts:   []mcp.Prompt{{Name: marker + "-prompt"}},
	}
}

func TestValkeyCapabilityStore_SetWritesReferenceAndSharedDocument(t *testing.T) {
	store, srv := newValkeyCapabilityStoreForTest(t, time.Hour)
	ctx := context.Background()
	caps := bigCapabilities("k8s")

	require.NoError(t, store.Set(ctx, "session-a", "mcp-kubernetes", caps))
	require.NoError(t, store.Set(ctx, "session-b", "mcp-kubernetes", caps))
	// The same document under a second server name of the same session.
	require.NoError(t, store.Set(ctx, "session-b", "other-mcp-kubernetes", caps))

	// The session hashes carry references, not documents.
	fieldA := srv.HGet("muster:cap:session-a", "mcp-kubernetes")
	fieldB := srv.HGet("muster:cap:session-b", "mcp-kubernetes")
	assert.True(t, strings.HasPrefix(fieldA, "sha256:"), "field is a reference: %q", fieldA)
	assert.Equal(t, fieldA, fieldB, "identical documents share one reference")
	assert.Less(t, len(fieldA), 80, "a reference is small")

	// Exactly one document for three fields across two sessions.
	blobs := srv.Keys()
	var docKeys []string
	for _, k := range blobs {
		if strings.HasPrefix(k, "muster:capblob:") {
			docKeys = append(docKeys, k)
		}
	}
	require.Len(t, docKeys, 1)
	assert.Equal(t, "muster:capblob:"+strings.TrimPrefix(fieldA, "sha256:"), docKeys[0])

	// Both the session hash and the document carry the store TTL.
	assert.InDelta(t, time.Hour.Seconds(), srv.TTL("muster:cap:session-a").Seconds(), 5)
	assert.InDelta(t, time.Hour.Seconds(), srv.TTL(docKeys[0]).Seconds(), 5)

	// Reads resolve the reference.
	for _, sess := range []string{"session-a", "session-b"} {
		got, err := store.Get(ctx, sess, "mcp-kubernetes")
		require.NoError(t, err)
		assertSameCapabilities(t, caps, got)
	}
	all, err := store.GetAll(ctx, "session-b")
	require.NoError(t, err)
	require.Len(t, all, 2)
	assertSameCapabilities(t, caps, all["other-mcp-kubernetes"])

	ok, err := store.Exists(ctx, "session-a", "mcp-kubernetes")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestValkeyCapabilityStore_DifferentDocumentsGetDifferentReferences(t *testing.T) {
	store, srv := newValkeyCapabilityStoreForTest(t, time.Hour)
	ctx := context.Background()

	require.NoError(t, store.Set(ctx, "s", "a", bigCapabilities("one")))
	require.NoError(t, store.Set(ctx, "s", "b", bigCapabilities("two")))
	assert.NotEqual(t, srv.HGet("muster:cap:s", "a"), srv.HGet("muster:cap:s", "b"))

	// Re-setting a server with new content leaves the old document to expire
	// on its own and points the field at the new one.
	require.NoError(t, store.Set(ctx, "s", "a", bigCapabilities("three")))
	got, err := store.Get(ctx, "s", "a")
	require.NoError(t, err)
	assert.Equal(t, "three-res", got.Resources[0].Name)
}

func TestValkeyCapabilityStore_ExpiredDocumentIsAMiss(t *testing.T) {
	writer, srv := newValkeyCapabilityStoreForTest(t, time.Hour)
	ctx := context.Background()

	require.NoError(t, writer.Set(ctx, "s", "gone", bigCapabilities("gone")))
	require.NoError(t, writer.Set(ctx, "s", "kept", bigCapabilities("kept")))
	ref := srv.HGet("muster:cap:s", "gone")
	srv.Del("muster:capblob:" + strings.TrimPrefix(ref, "sha256:"))

	// A process that never decoded the document -- a fresh store on the same
	// key space, as after a restart -- sees the miss.
	store := newValkeyCapabilityStoreOn(t, srv, time.Hour)
	got, err := store.Get(ctx, "s", "gone")
	require.NoError(t, err)
	assert.Nil(t, got, "a reference without its document reads as a cache miss")

	all, err := store.GetAll(ctx, "s")
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Contains(t, all, "kept")

	// The process that decoded the document keeps serving it for as long as
	// the session's hash references it: the reference names those exact
	// bytes, so nothing about the session's view has changed.
	got, err = writer.Get(ctx, "s", "gone")
	require.NoError(t, err)
	assert.NotNil(t, got, "the decoded document outlives its copy in Valkey")

	// The field itself is still there (Exists answers the hash), and the next
	// Set repairs it.
	ok, err := store.Exists(ctx, "s", "gone")
	require.NoError(t, err)
	assert.True(t, ok)
	require.NoError(t, store.Set(ctx, "s", "gone", bigCapabilities("gone")))
	got, err = store.Get(ctx, "s", "gone")
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestValkeyCapabilityStore_SetRefreshesTheDocumentTTL(t *testing.T) {
	store, srv := newValkeyCapabilityStoreForTest(t, time.Hour)
	ctx := context.Background()
	caps := bigCapabilities("shared")

	require.NoError(t, store.Set(ctx, "old-session", "srv", caps))
	ref := srv.HGet("muster:cap:old-session", "srv")
	doc := "muster:capblob:" + strings.TrimPrefix(ref, "sha256:")

	srv.FastForward(50 * time.Minute)
	assert.InDelta(t, (10 * time.Minute).Seconds(), srv.TTL(doc).Seconds(), 5)

	// A new session referencing the same document renews it; the old
	// session's own hash keeps its remaining time.
	require.NoError(t, store.Set(ctx, "new-session", "srv", caps))
	assert.InDelta(t, time.Hour.Seconds(), srv.TTL(doc).Seconds(), 5)
	assert.InDelta(t, (10 * time.Minute).Seconds(), srv.TTL("muster:cap:old-session").Seconds(), 5)
}

func TestValkeyCapabilityStore_ReadsLegacyInlineFields(t *testing.T) {
	store, srv := newValkeyCapabilityStoreForTest(t, time.Hour)
	ctx := context.Background()
	caps := bigCapabilities("legacy")
	inline, err := json.Marshal(caps)
	require.NoError(t, err)
	srv.HSet("muster:cap:s", "srv", string(inline))

	got, err := store.Get(ctx, "s", "srv")
	require.NoError(t, err)
	assertSameCapabilities(t, caps, got)

	all, err := store.GetAll(ctx, "s")
	require.NoError(t, err)
	require.Len(t, all, 1)
	assertSameCapabilities(t, caps, all["srv"])
}

func TestValkeyCapabilityStore_MigrateInlineEntries(t *testing.T) {
	store, srv := newValkeyCapabilityStoreForTest(t, time.Hour)
	ctx := context.Background()
	shared := bigCapabilities("shared")
	other := bigCapabilities("other")
	inlineShared, err := json.Marshal(shared)
	require.NoError(t, err)
	inlineOther, err := json.Marshal(other)
	require.NoError(t, err)

	// Two sessions written by the previous layout, one document in common;
	// one field already a reference; one field that is neither.
	srv.HSet("muster:cap:ext-1", "k8s", string(inlineShared))
	srv.HSet("muster:cap:ext-1", "prom", string(inlineOther))
	srv.HSet("muster:cap:ext-2", "k8s", string(inlineShared))
	srv.HSet("muster:cap:ext-2", "junk", "not json")
	require.NoError(t, store.Set(ctx, "ext-3", "k8s", shared))
	srv.SetTTL("muster:cap:ext-1", 20*time.Minute)
	srv.SetTTL("muster:cap:ext-2", 20*time.Minute)

	report, err := store.MigrateInlineEntries(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, report.Sessions)
	assert.Equal(t, 3, report.Migrated, "three inline fields rewritten")
	assert.Equal(t, 2, report.Documents, "two distinct documents")
	assert.Equal(t, 2*len(inlineShared)+len(inlineOther), report.InlineBytes)

	for _, key := range []string{"muster:cap:ext-1", "muster:cap:ext-2", "muster:cap:ext-3"} {
		assert.True(t, strings.HasPrefix(srv.HGet(key, "k8s"), "sha256:"), "%s k8s is a reference", key)
	}
	assert.Equal(t, srv.HGet("muster:cap:ext-1", "k8s"), srv.HGet("muster:cap:ext-3", "k8s"), "migrated and freshly written fields share the document")
	assert.Equal(t, "not json", srv.HGet("muster:cap:ext-2", "junk"), "an undecodable field is left alone")

	// Session TTLs are untouched by the migration.
	assert.InDelta(t, (20 * time.Minute).Seconds(), srv.TTL("muster:cap:ext-1").Seconds(), 5)

	// Everything still reads.
	got, err := store.Get(ctx, "ext-1", "prom")
	require.NoError(t, err)
	assertSameCapabilities(t, other, got)
	all, err := store.GetAll(ctx, "ext-2")
	require.NoError(t, err)
	assert.Len(t, all, 1, "the junk field is skipped, k8s resolves")

	// Idempotent.
	again, err := store.MigrateInlineEntries(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, again.Migrated)
	assert.Equal(t, 3, again.Sessions)
}

func TestValkeyCapabilityStore_DeleteServerAndListSessions(t *testing.T) {
	store, srv := newValkeyCapabilityStoreForTest(t, time.Hour)
	ctx := context.Background()
	caps := bigCapabilities("c")

	for _, sess := range []string{"a", "b", "c"} {
		require.NoError(t, store.Set(ctx, sess, "srv", caps))
		require.NoError(t, store.Set(ctx, sess, "other", caps))
	}
	sessions, err := store.ListSessions(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, sessions)

	require.NoError(t, store.DeleteServer(ctx, "srv"))
	for _, sess := range []string{"a", "b", "c"} {
		assert.False(t, srv.Exists("muster:cap:"+sess) && srv.HGet("muster:cap:"+sess, "srv") != "", "srv removed from %s", sess)
		assert.NotEmpty(t, srv.HGet("muster:cap:"+sess, "other"))
	}

	require.NoError(t, store.DeleteEntry(ctx, "a", "other"))
	require.NoError(t, store.Delete(ctx, "b"))
	sessions, err = store.ListSessions(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"c"}, sessions, "a is empty (deleted by Valkey), b deleted")

	touched, err := store.Touch(ctx, "c")
	require.NoError(t, err)
	assert.True(t, touched)
	touched, err = store.Touch(ctx, "b")
	require.NoError(t, err)
	assert.False(t, touched)
}

// A session listing costs one HGETALL and fetches each distinct document once
// per process: the second listing of the same session, and every listing of
// another session that references the same documents, reads only the hash.
// Before #1225 every listing read every server's entry with its own HGET and
// GET (one round trip each) and decoded the document again.
func TestValkeyCapabilityStore_GetAllReadsEachDocumentOnce(t *testing.T) {
	store, srv := newValkeyCapabilityStoreForTest(t, time.Hour)
	ctx := context.Background()

	shared := bigCapabilities("shared")
	require.NoError(t, store.Set(ctx, "s1", "mc1-mcp-kubernetes", shared))
	require.NoError(t, store.Set(ctx, "s1", "mc2-mcp-kubernetes", shared)) // same document
	require.NoError(t, store.Set(ctx, "s1", "mc1-mcp-prometheus", bigCapabilities("prom")))
	require.NoError(t, store.Set(ctx, "s2", "mc1-mcp-kubernetes", shared))

	// A process that decoded nothing yet: one HGETALL plus one GET per
	// distinct document (two, not three).
	fresh := newValkeyCapabilityStoreOn(t, srv, time.Hour)
	before := srv.CommandCount()
	all, err := fresh.GetAll(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, 3, srv.CommandCount()-before, "HGETALL + one GET per distinct document")

	// The same session again: the hash only.
	before = srv.CommandCount()
	all, err = fresh.GetAll(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, 1, srv.CommandCount()-before, "a warm listing is one HGETALL")
	assertSameCapabilities(t, shared, all["mc2-mcp-kubernetes"])

	// Another session referencing a document this process already decoded.
	before = srv.CommandCount()
	all, err = fresh.GetAll(ctx, "s2")
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, 1, srv.CommandCount()-before, "documents are shared across sessions")

	// A single entry read the same way: the HGET only.
	before = srv.CommandCount()
	got, err := fresh.Get(ctx, "s1", "mc1-mcp-prometheus")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 1, srv.CommandCount()-before, "a warm Get is one HGET")

	// The process that wrote the documents never needed the GETs at all.
	before = srv.CommandCount()
	all, err = store.GetAll(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, 1, srv.CommandCount()-before, "Set primes the document cache")
}

// A server that lists different tools writes a new reference; the next
// listing fetches that document once and serves the new tools.
func TestValkeyCapabilityStore_GetAllFollowsARewrittenReference(t *testing.T) {
	store, srv := newValkeyCapabilityStoreForTest(t, time.Hour)
	ctx := context.Background()
	reader := newValkeyCapabilityStoreOn(t, srv, time.Hour)

	require.NoError(t, store.Set(ctx, "s", "srv", bigCapabilities("v1")))
	all, err := reader.GetAll(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, "v1-res", all["srv"].Resources[0].Name)

	require.NoError(t, store.Set(ctx, "s", "srv", bigCapabilities("v2")))
	before := srv.CommandCount()
	all, err = reader.GetAll(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, "v2-res", all["srv"].Resources[0].Name)
	assert.Equal(t, 2, srv.CommandCount()-before, "HGETALL + the GET of the new document")
}

// What a reader gets is its own: modifying it does not change what the next
// reader of the same document sees.
func TestValkeyCapabilityStore_ReadersGetTheirOwnCopy(t *testing.T) {
	store, _ := newValkeyCapabilityStoreForTest(t, time.Hour)
	ctx := context.Background()
	require.NoError(t, store.Set(ctx, "s", "srv", &Capabilities{Tools: []mcp.Tool{{Name: "a"}, {Name: "b"}}}))

	first, err := store.Get(ctx, "s", "srv")
	require.NoError(t, err)
	first.Tools = first.Tools[:1]
	first.Tools[0].Name = "changed"

	second, err := store.Get(ctx, "s", "srv")
	require.NoError(t, err)
	require.Len(t, second.Tools, 2)
	assert.Equal(t, "a", second.Tools[0].Name)
}
