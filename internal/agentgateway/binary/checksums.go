package binary

// pinnedChecksums maps "<asset>/<version>" to the hex-encoded SHA-256 of
// the corresponding agentgateway release asset. Entries are sourced from
// the upstream GitHub release page and verified by re-computing SHA-256
// over the downloaded binary; see `make verify-checksums`.
var pinnedChecksums = map[string]string{
	"agentgateway-linux-amd64/1.2.1":       "90f549c7f6ce93d65b6a6708c9aafac8f935e3045d3d035766f713bc850c3c3a",
	"agentgateway-linux-arm64/1.2.1":       "bbdf5ac36d531df6e1f3b54b8b6232e240883392e369f405ee5cca951d8c1a82",
	"agentgateway-darwin-arm64/1.2.1":      "e785ea8bd84be92f48eb0865643bb803e00f78b76244cf411a09e37fe179605f",
	"agentgateway-windows-amd64.exe/1.2.1": "6e517cf2f19c0fa7557a97cb26530f6ffc9e258a23b2105c9c3138c23ef2702f",
}

// pinnedChecksum returns the pinned SHA-256 hex for asset at version,
// and whether an entry exists.
func pinnedChecksum(asset, version string) (string, bool) {
	digest, ok := pinnedChecksums[asset+"/"+version]
	return digest, ok
}
