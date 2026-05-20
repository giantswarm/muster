package binary

// pinnedChecksums maps the upstream asset filename to the hex-encoded
// SHA-256 of that asset for the agentgateway release named by
// PinnedVersion (driven by go.mod). Regenerate with
// `make refresh-checksums` after `go get github.com/agentgateway/agentgateway@vX.Y.Z`.
var pinnedChecksums = map[string]string{
	"agentgateway-linux-amd64":       "90f549c7f6ce93d65b6a6708c9aafac8f935e3045d3d035766f713bc850c3c3a",
	"agentgateway-linux-arm64":       "bbdf5ac36d531df6e1f3b54b8b6232e240883392e369f405ee5cca951d8c1a82",
	"agentgateway-darwin-arm64":      "e785ea8bd84be92f48eb0865643bb803e00f78b76244cf411a09e37fe179605f",
	"agentgateway-windows-amd64.exe": "6e517cf2f19c0fa7557a97cb26530f6ffc9e258a23b2105c9c3138c23ef2702f",
}
