# muster version

Display the version number of Muster.

## Synopsis

```
muster version
```

## Description

The `version` command displays the current version of the Muster application. This is useful for:
- Verifying which version is currently installed
- Troubleshooting compatibility issues
- Checking if updates are needed
- Including version information in bug reports

The version information includes the semantic version number (e.g., `v1.2.3`) if available, or `dev` for development builds.

## Options

This command has no additional options beyond the standard help flag.

## Examples

### Basic Usage
```bash
# Display current version
muster version
# Output: muster version v1.2.3

# Development builds show:
muster version
# Output: muster version dev
```

### Scripting Usage
```bash
# Extract just the version number for scripts
VERSION=$(muster version | cut -d' ' -f3)
echo "Running Muster $VERSION"

# Check if running a development version
if muster version | grep -q "dev"; then
    echo "Warning: Running development version"
fi
```

### Troubleshooting
```bash
# Include version in bug reports
echo "Muster Version: $(muster version)"
echo "OS: $(uname -a)"
echo "Go Version: $(go version)"
```

## Version Information

The version displayed follows semantic versioning (SemVer) principles:
- **Major.Minor.Patch** (e.g., `v1.2.3`)
- **Development builds**: Show as `dev`
- **Release candidates**: May include suffixes like `v1.2.3-rc1`

## Related Commands

- [`muster self-update`](self-update.md) - Update to the latest version
- `muster --version` - Alternative way to display version (same output)

## Notes

- The version is set at build time and embedded in the binary
- Development builds cannot be updated using `muster self-update`
- The version information is also available via the `--version` flag on the root command 