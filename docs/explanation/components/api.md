# Central API Layer

## Purpose
Acts as service locator and defines interfaces for all components.

## Key Responsibilities
- Handler interface definitions
- Service registration and retrieval
- Type definitions for requests/responses
- Error handling definitions

## Key Files
- `handlers.go`: Interface definitions
- `types.go`: Core types
- `requests.go`: Request structures
- `errors.go`: Error types
- `orchestrator.go`: Coordination logic

## Integration
All components register adapters here. No dependencies on other packages. 