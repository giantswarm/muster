// Package binary resolves an executable agentgateway binary at the
// version this build of muster targets, for filesystem mode.
//
// Resolution order:
//
//  1. MUSTER_AGW_BINARY env var. If set, the named file is used
//     directly; nothing else is attempted.
//  2. Cached binary under BaseDir (default ~/.config/muster/bin).
//  3. Download from the agentgateway GitHub release at PinnedVersion,
//     verify its SHA256 against the .sha256 file at the same release,
//     and install atomically into BaseDir.
//
// Step 3 is suppressed by WithNoDownload, in which case a cache miss
// returns ErrBinaryNotFound. The package has no knowledge of
// MCPServer, the muster service locator, or process lifecycle.
package binary
