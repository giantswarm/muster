# muster self-update

Update Muster to the latest version from GitHub.

## Synopsis

```
muster self-update
```

## Description

The `self-update` command automatically checks for the latest release of Muster on GitHub and updates the current binary if a newer version is available. This provides a convenient way to keep Muster up-to-date without manually downloading and installing new versions.

**Update Process:**
1. Checks the current version against the latest GitHub release
2. Downloads the appropriate binary for your platform
3. Replaces the current executable with the new version
4. Displays release notes and confirmation

**Limitations:**
- Cannot update development versions (`dev`)
- Requires internet connection to GitHub
- Requires write permissions to the executable location

## Options

This command has no additional options beyond the standard help flag.

## Examples

### Basic Update
```bash
# Check for and install updates
muster self-update

# Example output:
# Current version: v1.2.3
# Checking for updates...
# Found newer version: v1.3.0 (published at 2024-01-15T10:30:00Z)
# Release notes:
# - Added new workflow features
# - Fixed service lifecycle bugs
# - Improved error handling
# 
# Updating /usr/local/bin/muster to version v1.3.0...
# Successfully updated to version v1.3.0
```

### Already Up-to-Date
```bash
muster self-update
# Output: Current version is the latest.
```

### Development Version
```bash
# Attempting to update a development build
muster self-update
# Error: cannot self-update a development version
```

## Update Behavior

### Version Comparison
The command uses semantic versioning to determine if an update is needed:
- `v1.2.3` → `v1.2.4`: Patch update (bug fixes)
- `v1.2.3` → `v1.3.0`: Minor update (new features)
- `v1.2.3` → `v2.0.0`: Major update (breaking changes)

### Binary Replacement
The update process:
1. Downloads the new binary to a temporary location
2. Verifies the download integrity
3. Replaces the current executable atomically
4. Cleans up temporary files

### Platform Detection
Automatically detects and downloads the correct binary for:
- Operating system (Linux, macOS, Windows)
- Architecture (amd64, arm64, etc.)

## Troubleshooting

### Permission Issues
```bash
# If the binary location requires elevated permissions
sudo muster self-update

# Or move muster to a user-writable location:
mkdir -p ~/bin
cp $(which muster) ~/bin/
export PATH="$HOME/bin:$PATH"
muster self-update
```

### Network Issues
```bash
# Check internet connectivity
curl -I https://api.github.com/repos/giantswarm/muster/releases/latest

# Use with proxy if needed
export HTTP_PROXY=http://proxy.example.com:8080
export HTTPS_PROXY=http://proxy.example.com:8080
muster self-update
```

### Manual Verification
```bash
# Check current version before update
muster version

# Perform update
muster self-update

# Verify new version after update
muster version

# Test basic functionality
muster --help
```

## Security Considerations

- Downloads are verified against GitHub's release checksums
- Only official releases from the `giantswarm/muster` repository are used
- The update process requires explicit user execution (no automatic updates)
- Binary signatures are validated when available

## Alternative Update Methods

If `self-update` doesn't work for your environment:

### Package Managers
```bash
# Using go install (requires Go toolchain)
go install github.com/giantswarm/muster@latest

# Using package managers (if available)
brew upgrade muster
apt update && apt upgrade muster
```

### Manual Download
```bash
# Download from GitHub releases
curl -L -o muster https://github.com/giantswarm/muster/releases/latest/download/muster-linux-amd64
chmod +x muster
sudo mv muster /usr/local/bin/
```

## Related Commands

- [`muster version`](version.md) - Check current version before updating
- `muster --help` - Verify installation after update

## Notes

- The self-update feature requires the binary to be built with release tags
- Development builds and custom builds cannot use self-update
- Updates preserve your configuration and data files
- The command will display release notes for the new version when available 