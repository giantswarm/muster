# ADR-003: Configuration Management

## Status
Accepted

## Context

Muster needs a flexible configuration system that can:
- Load configuration from a predictable location
- Support different entity types (mcpservers, workflows, serviceclasses, services)
- Be easily editable by users
- Support both API-driven and file-based configuration management

## Decision

Use direct YAML file loading with a standardized directory structure at `~/.config/muster/`:

- **Main config**: `config.yaml` - Core muster settings
- **Entity directories**: Separate subdirectories for each entity type
  - `mcpservers/` - MCP server definitions
  - `workflows/` - Workflow definitions  
  - `serviceclasses/` - ServiceClass definitions
  - `services/` - Service instance definitions

Implementation uses `gopkg.in/yaml.v3` for direct YAML parsing without additional configuration libraries.

## Consequences

### Positive
- Simple, predictable file structure
- Easy to edit manually with any text editor
- No additional dependencies
- Clear separation between entity types
- API can read/write the same files users edit

### Negative
- No built-in environment variable overrides
- Manual file structure management
- Limited advanced configuration features

## Implementation

```go
// Configuration loading
func LoadConfig() (MusterConfig, error) {
    userConfigDir, err := GetUserConfigDir() // ~/.config/muster
    return LoadConfigFromPath(userConfigDir)
}

// Entity storage
type Storage struct {
    configPath string // ~/.config/muster or custom path
}

func (s *Storage) Save(entityType, name string, data []byte) error {
    targetDir := filepath.Join(s.configPath, entityType)
    filePath := filepath.Join(targetDir, name+".yaml")
    return os.WriteFile(filePath, data, 0644)
}
``` 