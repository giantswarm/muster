# muster serve

Start the Muster control plane server.

## Synopsis

```
muster serve [OPTIONS]
```

## Description

The `serve` command starts the Muster control plane, which manages MCP servers, workflows, services, and provides the unified API for agent interactions. This is the core command that enables all other Muster functionality.

The aggregator server provides a unified MCP interface that other muster commands can connect to. Once started, you can use commands like `muster list`, `muster create`, `muster start`, etc. to interact with the running server.

## Options

### Server Configuration
- `--config-path` (string): Custom configuration directory path
  - Default: `~/.config/muster`
  - Directory should contain `config.yaml` and subdirectories: `mcpservers/`, `workflows/`, `serviceclasses/`, `services/`

### Logging and Debugging
- `--debug`: Enable debug-level logging and verbose output
  - Default: `false`
  - Provides detailed information about service startup and operations
- `--silent`: Disable all output to the console
  - Default: `false`
  - Useful for programmatic usage or when output needs to be suppressed

### Security and Safety
- `--yolo`: Disable denylist for destructive tool calls
  - Default: `false`
  - **WARNING**: Use with extreme caution. This removes safety restrictions on potentially destructive operations

## Configuration

The serve command uses configuration from `config.yaml` in the configuration directory:

```yaml
# Default configuration structure
server:
  port: 8080
  host: "0.0.0.0"
  timeout: "30s"

mcpServers:
  autoStart: true
  timeout: "30s"
  healthCheckInterval: "30s"

logging:
  level: "info"
  format: "text"

storage:
  dataDir: "~/.local/share/muster"
  tempDir: "/tmp"

# Component directories
directories:
  mcpservers: "mcpservers/"
  workflows: "workflows/"
  serviceclasses: "serviceclasses/"
  services: "services/"
```

## Startup Sequence

When you run `muster serve`, the following initialization occurs:

1. **Configuration Loading**
   - Loads configuration from the specified directory
   - Supports layered configuration (defaults + user + project)
   - Validates configuration structure

2. **Service Initialization**
   - Sets up API service locator pattern
   - Initializes orchestrator for service lifecycle management
   - Registers all service adapters with the API layer

3. **Component Loading**
   - Loads MCP server definitions from `mcpservers/`
   - Loads workflow definitions from `workflows/`
   - Loads service class templates from `serviceclasses/`
   - Loads service instances from `services/`

4. **Auto-Start Services**
   - Starts all configured MCP servers with `autoStart: true`
   - Initializes the aggregator service for tool access
   - Starts any service instances marked for auto-start

5. **Server Ready**
   - Begins accepting connections on the configured port
   - Provides unified MCP interface for client connections

## Examples

### Basic Usage
```bash
# Start with default settings
muster serve

# Logs will show startup progress:
# INFO: Setting up orchestrator for service management
# INFO: Loading MCP servers from ~/.config/muster/mcpservers/
# INFO: Services started. Press Ctrl+C to stop all services and exit.
```

### Custom Configuration Directory
```bash
# Use custom configuration path
muster serve --config-path /etc/muster

# For development with local config
muster serve --config-path ./local-config
```

### Debug Mode
```bash
# Start with debug logging
muster serve --debug

# Debug output includes:
# DEBUG: Loaded configuration from custom path: /etc/muster
# DEBUG: Initializing service registry
# DEBUG: Registering MCP server adapter
# DEBUG: Starting MCP server: kubernetes
```

### Production Setup
```bash
# Production server configuration
muster serve \
  --config-path /etc/muster \
  --silent

# Recommended systemd service setup
```

### Development Mode
```bash
# Development with debug output
muster serve \
  --debug \
  --config-path ./dev-config
```

## Configuration File Structure

The configuration directory should be organized as follows:

```
/path/to/config/
├── config.yaml              # Main configuration
├── mcpservers/              # MCP server definitions
│   ├── kubernetes.yaml
│   ├── prometheus.yaml
│   └── github.yaml
├── workflows/               # Workflow definitions  
│   ├── deploy-app.yaml
│   └── backup-db.yaml
├── serviceclasses/          # Service templates
│   ├── web-app.yaml
│   └── database.yaml
└── services/                # Service instances
    ├── my-web-app.yaml
    └── prod-database.yaml
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MUSTER_CONFIG_PATH` | Override default configuration directory | `~/.config/muster` |
| `MUSTER_DEBUG` | Enable debug mode | `false` |
| `MUSTER_LOG_LEVEL` | Set log level (debug\|info\|warn\|error) | `info` |

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Server shutdown cleanly |
| 1 | Configuration error or invalid arguments |
| 2 | Service initialization failed |
| 3 | Failed to start required services |
| 130 | Interrupted by signal (Ctrl+C) |

## Signals

The serve command handles the following signals for graceful shutdown:

- **SIGINT (Ctrl+C)**: Initiates graceful shutdown sequence
- **SIGTERM**: Initiates graceful shutdown sequence

During shutdown:
1. Stops accepting new connections
2. Gracefully stops all running services
3. Saves any pending state
4. Exits cleanly

## Troubleshooting

### Configuration Errors

**Error**: `Failed to load muster configuration`
```bash
# Check configuration file exists and is valid YAML
ls -la ~/.config/muster/config.yaml
muster serve --debug  # Shows detailed error information
```

**Error**: `Failed to initialize services`
```bash
# Verify configuration directory structure
ls -la ~/.config/muster/
mkdir -p ~/.config/muster/{mcpservers,workflows,serviceclasses,services}
```

### MCP Server Issues

**Error**: MCP servers fail to start
```bash
# Check MCP server configurations
muster serve --debug

# Verify MCP server binaries are available
which mcp-kubernetes
which mcp-prometheus
```

### Port Conflicts

**Error**: Address already in use
```bash
# Check what's using the port
netstat -tulpn | grep :8080
lsof -i :8080

# Edit config.yaml to use different port
# server:
#   port: 8081
```

### Permission Issues

**Error**: Permission denied accessing configuration
```bash
# Check directory permissions
ls -la ~/.config/muster
chmod 755 ~/.config/muster
chmod 644 ~/.config/muster/config.yaml

# For system-wide configuration
sudo chown -R muster:muster /etc/muster
```

### Service Dependencies

**Error**: Services fail to start due to dependencies
```bash
# Start without auto-start to debug
# Edit config.yaml:
# mcpServers:
#   autoStart: false

# Then start services individually using other commands
muster list mcpserver
muster start service <service-name>
```

## Integration with Other Commands

After starting the server, you can use other Muster commands:

```bash
# In one terminal
muster serve

# In another terminal
muster list service           # List all services
muster create service my-app web-service
muster agent --repl          # Interactive tool exploration
muster test --scenario basic-crud  # Run tests
```

## IDE Integration

To connect Muster to your IDE (Cursor, VSCode, etc.):

```bash
# Start the server
muster serve

# In your IDE's MCP configuration, use:
muster agent --mcp-server
```

## Related Commands

- **[agent](agent.md)** - Connect to the server interactively
- **[list](list.md)** - List resources managed by the server
- **[get](get.md)** - Get detailed resource information
- **[create](create.md)** - Create new resources
- **[test](test.md)** - Test server functionality

## Advanced Usage

### Custom Configuration Loading

```bash
# Single-path configuration (no layering)
muster serve --config-path /opt/muster/config

# Development with debug and custom path
muster serve --debug --config-path ./test-config
```

### Security Considerations

```bash
# Production: Run without --yolo flag
muster serve --config-path /etc/muster

# Development: Use --yolo for testing only
muster serve --debug --yolo --config-path ./dev-config
```

### Monitoring and Health Checks

The server exposes health and status information:

```bash
# Check server status (from another terminal)
curl http://localhost:8080/api/v1/status

# Monitor logs in real-time
muster serve --debug | tee muster.log
``` 