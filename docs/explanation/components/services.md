# Service Management Component

## Purpose
Handles lifecycle management of static service instances (the aggregator and the per-MCPServer service wrappers).

## Key Responsibilities
- Service registry and instance tracking
- State management (start/stop/restart)
- Status and health monitoring

## Key Files
- `registry.go`: Service registration
- `interfaces.go`: Service interfaces

## Integration
Uses API adapters to integrate with the orchestrator and aggregator.
