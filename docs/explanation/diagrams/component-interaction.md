# Component Interaction Diagram

```mermaid
sequenceDiagram
    participant Agent
    participant MusterAgent
    participant API
    participant Aggregator
    participant MCPServer
    Agent->>MusterAgent: Discover tools
    MusterAgent->>API: GetAggregator
    API->>Aggregator: Forward request
    Aggregator->>MCPServer: Query tools
    MCPServer-->>Aggregator: Tool list
    Aggregator-->>MusterAgent: Filtered tools
    MusterAgent-->>Agent: Response
``` 