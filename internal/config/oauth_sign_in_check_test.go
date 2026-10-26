package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateSignInCheckOrigin(t *testing.T) {
	require.NoError(t, ValidateSignInCheckOrigin("https://muster.example.com", "https://muster.example.com/"))
	require.NoError(t, ValidateSignInCheckOrigin("https://muster.example.com/", "https://muster.example.com"))
	require.Error(t, ValidateSignInCheckOrigin("https://proxy.example.com", "https://muster.example.com"))
	require.Error(t, ValidateSignInCheckOrigin("http://muster.example.com", "https://muster.example.com"))
	require.Error(t, ValidateSignInCheckOrigin("https://muster.example.com:8443", "https://muster.example.com"))
}
