# MCP Server Management Component

## Purpose
Manages individual MCP server processes and their lifecycles.

## Key Responsibilities
- Process starting/stopping
- Configuration management
- Health checking
- Client communication

## Key Files
- `api_adapter.go`: API integration
- `client.go`: Server communication
- `process.go`: Process management
- `types.go`: Configuration types

## Integration
Registers with API and aggregator. Used by services for dependent servers. 