package mcpserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"muster/internal/api"
)

func TestCredentialsAdapter_LoadClientCredentials(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	t.Run("loads credentials successfully with default keys", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-credentials",
				Namespace: "muster",
			},
			Data: map[string][]byte{
				"client-id":     []byte("my-client-id"),
				"client-secret": []byte("my-client-secret"),
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		adapter := NewCredentialsAdapter(k8sClient)

		ctx := context.Background()
		secretRef := &api.ClientCredentialsSecretRef{
			Name: "test-credentials",
		}

		credentials, err := adapter.LoadClientCredentials(ctx, secretRef, "muster")

		require.NoError(t, err)
		assert.Equal(t, "my-client-id", credentials.ClientID)
		assert.Equal(t, "my-client-secret", credentials.ClientSecret)
	})

	t.Run("loads credentials with custom keys", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "custom-credentials",
				Namespace: "custom-ns",
			},
			Data: map[string][]byte{
				"oauth-client-id": []byte("custom-id"),
				"oauth-secret":    []byte("custom-secret"),
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		adapter := NewCredentialsAdapter(k8sClient)

		ctx := context.Background()
		secretRef := &api.ClientCredentialsSecretRef{
			Name:            "custom-credentials",
			Namespace:       "custom-ns",
			ClientIDKey:     "oauth-client-id",
			ClientSecretKey: "oauth-secret",
		}

		credentials, err := adapter.LoadClientCredentials(ctx, secretRef, "muster")

		require.NoError(t, err)
		assert.Equal(t, "custom-id", credentials.ClientID)
		assert.Equal(t, "custom-secret", credentials.ClientSecret)
	})

	t.Run("uses default namespace when not specified", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-credentials",
				Namespace: "default-ns",
			},
			Data: map[string][]byte{
				"client-id":     []byte("id"),
				"client-secret": []byte("secret"),
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		adapter := NewCredentialsAdapter(k8sClient)

		ctx := context.Background()
		secretRef := &api.ClientCredentialsSecretRef{
			Name: "test-credentials",
			// Namespace not specified
		}

		credentials, err := adapter.LoadClientCredentials(ctx, secretRef, "default-ns")

		require.NoError(t, err)
		assert.Equal(t, "id", credentials.ClientID)
		assert.Equal(t, "secret", credentials.ClientSecret)
	})

	t.Run("returns error when secret not found", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		adapter := NewCredentialsAdapter(k8sClient)

		ctx := context.Background()
		secretRef := &api.ClientCredentialsSecretRef{
			Name:      "nonexistent-secret",
			Namespace: "muster",
		}

		_, err := adapter.LoadClientCredentials(ctx, secretRef, "muster")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get secret")
	})

	t.Run("returns error when client-id key missing", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "incomplete-secret",
				Namespace: "muster",
			},
			Data: map[string][]byte{
				"client-secret": []byte("secret"),
				// client-id missing
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		adapter := NewCredentialsAdapter(k8sClient)

		ctx := context.Background()
		secretRef := &api.ClientCredentialsSecretRef{
			Name: "incomplete-secret",
		}

		_, err := adapter.LoadClientCredentials(ctx, secretRef, "muster")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required key 'client-id'")
	})

	t.Run("returns error when client-secret key missing", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "incomplete-secret",
				Namespace: "muster",
			},
			Data: map[string][]byte{
				"client-id": []byte("id"),
				// client-secret missing
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		adapter := NewCredentialsAdapter(k8sClient)

		ctx := context.Background()
		secretRef := &api.ClientCredentialsSecretRef{
			Name: "incomplete-secret",
		}

		_, err := adapter.LoadClientCredentials(ctx, secretRef, "muster")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required key 'client-secret'")
	})

	t.Run("returns error when client-id is empty", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "empty-values-secret",
				Namespace: "muster",
			},
			Data: map[string][]byte{
				"client-id":     []byte(""),
				"client-secret": []byte("secret"),
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		adapter := NewCredentialsAdapter(k8sClient)

		ctx := context.Background()
		secretRef := &api.ClientCredentialsSecretRef{
			Name: "empty-values-secret",
		}

		_, err := adapter.LoadClientCredentials(ctx, secretRef, "muster")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty value for key 'client-id'")
	})

	t.Run("returns error when secret reference is nil", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		adapter := NewCredentialsAdapter(k8sClient)

		ctx := context.Background()

		_, err := adapter.LoadClientCredentials(ctx, nil, "muster")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "secret reference is nil")
	})

	t.Run("returns error when secret name is empty", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		adapter := NewCredentialsAdapter(k8sClient)

		ctx := context.Background()
		secretRef := &api.ClientCredentialsSecretRef{
			Name: "",
		}

		_, err := adapter.LoadClientCredentials(ctx, secretRef, "muster")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "secret name is required")
	})

	t.Run("falls back to 'default' namespace when both secretRef.Namespace and defaultNamespace are empty", func(t *testing.T) {
		// Create secret in the "default" namespace
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "fallback-credentials",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"client-id":     []byte("fallback-id"),
				"client-secret": []byte("fallback-secret"),
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		adapter := NewCredentialsAdapter(k8sClient)

		ctx := context.Background()
		secretRef := &api.ClientCredentialsSecretRef{
			Name: "fallback-credentials",
			// Namespace not specified
		}

		// Both secretRef.Namespace and defaultNamespace are empty
		credentials, err := adapter.LoadClientCredentials(ctx, secretRef, "")

		require.NoError(t, err)
		assert.Equal(t, "fallback-id", credentials.ClientID)
		assert.Equal(t, "fallback-secret", credentials.ClientSecret)
	})
}

func TestCredentialsAdapter_Register(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	adapter := NewCredentialsAdapter(k8sClient)

	// Register the adapter
	adapter.Register()
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(nil) })

	// Verify it was registered
	handler := api.GetSecretCredentialsHandler()
	assert.NotNil(t, handler)
	assert.Equal(t, adapter, handler)
}
