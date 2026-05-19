package binary

import "fmt"

const (
	goosLinux   = "linux"
	goosDarwin  = "darwin"
	goosWindows = "windows"
	archAMD64   = "amd64"
	archARM64   = "arm64"
)

type platform struct{ os, arch string }

var supportedAssets = map[platform]string{
	{goosLinux, archAMD64}:   "agentgateway-linux-amd64",
	{goosLinux, archARM64}:   "agentgateway-linux-arm64",
	{goosDarwin, archARM64}:  "agentgateway-darwin-arm64",
	{goosWindows, archAMD64}: "agentgateway-windows-amd64.exe",
}

// assetForPlatform returns the upstream asset filename for goos/goarch,
// or ErrUnsupportedPlatform wrapped with the requested pair.
func assetForPlatform(goos, goarch string) (string, error) {
	asset, ok := supportedAssets[platform{goos, goarch}]
	if !ok {
		return "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, goos, goarch)
	}
	return asset, nil
}

// cacheFilename returns the version-suffixed filename used inside
// BaseDir for goos.
func cacheFilename(goos string) string {
	if goos == goosWindows {
		return "agentgateway-v" + PinnedVersion + ".exe"
	}
	return "agentgateway-v" + PinnedVersion
}
