package api

import (
	"context"
	"testing"
)

func TestSessionToolMemo_ResolveBuildsOnce(t *testing.T) {
	memo := &SessionToolMemo{}
	calls := 0
	build := func() map[string]struct{} {
		calls++
		return map[string]struct{}{"a": {}, "b": {}}
	}

	first := memo.Resolve(build)
	second := memo.Resolve(build)

	if calls != 1 {
		t.Fatalf("build invoked %d times, want exactly 1", calls)
	}
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("resolved sets have unexpected size: first=%d second=%d", len(first), len(second))
	}
}

func TestSessionToolMemoFromContext_AbsentReturnsNil(t *testing.T) {
	if memo := SessionToolMemoFromContext(context.Background()); memo != nil {
		t.Fatalf("expected nil memo on a bare context, got %v", memo)
	}
}

func TestWithSessionToolMemo_PresentAndIdempotent(t *testing.T) {
	ctx := WithSessionToolMemo(context.Background())
	memo := SessionToolMemoFromContext(ctx)
	if memo == nil {
		t.Fatal("expected a memo to be present after WithSessionToolMemo")
	}

	// Re-wrapping must not replace the existing memo, so a shared request keeps
	// one cache.
	ctx2 := WithSessionToolMemo(ctx)
	if got := SessionToolMemoFromContext(ctx2); got != memo {
		t.Fatal("WithSessionToolMemo must be idempotent and reuse the existing memo")
	}
}
