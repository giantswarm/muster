package binary

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAssetForPlatform(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
		wantErr      error
	}{
		{goosLinux, archAMD64, "agentgateway-linux-amd64", nil},
		{goosLinux, archARM64, "agentgateway-linux-arm64", nil},
		{goosDarwin, archARM64, "agentgateway-darwin-arm64", nil},
		{goosWindows, archAMD64, "agentgateway-windows-amd64.exe", nil},
		{goosDarwin, archAMD64, "", ErrUnsupportedPlatform},
		{goosLinux, "arm", "", ErrUnsupportedPlatform},
		{goosLinux, "ppc64le", "", ErrUnsupportedPlatform},
		{goosLinux, "s390x", "", ErrUnsupportedPlatform},
		{goosLinux, "riscv64", "", ErrUnsupportedPlatform},
		{"freebsd", archAMD64, "", ErrUnsupportedPlatform},
		{"plan9", archAMD64, "", ErrUnsupportedPlatform},
		{"", "", "", ErrUnsupportedPlatform},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.goarch, func(t *testing.T) {
			got, err := assetForPlatform(tc.goos, tc.goarch)
			if tc.wantErr != nil {
				require.Error(t, err)
				require.True(t, errors.Is(err, tc.wantErr), "got %v, want errors.Is %v", err, tc.wantErr)
				require.Contains(t, err.Error(), tc.goos)
				require.Contains(t, err.Error(), tc.goarch)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestCacheFilename(t *testing.T) {
	require.Equal(t, "agentgateway-v"+PinnedVersion, cacheFilename(goosLinux))
	require.Equal(t, "agentgateway-v"+PinnedVersion, cacheFilename(goosDarwin))
	require.Equal(t, "agentgateway-v"+PinnedVersion+".exe", cacheFilename(goosWindows))
}
