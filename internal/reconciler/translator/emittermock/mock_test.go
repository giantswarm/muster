package emittermock_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/reconciler/translator"
	"github.com/giantswarm/muster/internal/reconciler/translator/emittermock"
)

func TestEmitter_RecordsCalls(t *testing.T) {
	t.Parallel()

	mock := emittermock.New()
	require.Empty(t, mock.Calls())

	require.NoError(t, mock.Emit(t.Context(), translator.Model{}))
	require.NoError(t, mock.Emit(t.Context(), translator.Model{
		Backends: []translator.Backend{{Name: "srv"}},
	}))

	calls := mock.Calls()
	require.Len(t, calls, 2)
	require.Empty(t, calls[0].Backends)
	require.Equal(t, "srv", calls[1].Backends[0].Name)
}

func TestEmitter_SetError(t *testing.T) {
	t.Parallel()

	mock := emittermock.New()
	want := errors.New("boom")
	mock.SetError(want)

	err := mock.Emit(t.Context(), translator.Model{})
	require.ErrorIs(t, err, want)
	require.Len(t, mock.Calls(), 1, "Emit must record even when returning an error")

	mock.SetError(nil)
	require.NoError(t, mock.Emit(t.Context(), translator.Model{}))
}

func TestEmitter_ContextCanceled(t *testing.T) {
	t.Parallel()

	mock := emittermock.New()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := mock.Emit(ctx, translator.Model{})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, mock.Calls(), "canceled Emit must not record")
}

func TestEmitter_Reset(t *testing.T) {
	t.Parallel()

	mock := emittermock.New()
	mock.SetError(errors.New("sticky"))

	require.Error(t, mock.Emit(t.Context(), translator.Model{}))
	require.Len(t, mock.Calls(), 1)

	mock.Reset()
	require.Empty(t, mock.Calls())
	require.Error(t, mock.Emit(t.Context(), translator.Model{}), "Reset must preserve the configured error")
}
