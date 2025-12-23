# 007. CRD Status Reconciliation and Session-Aware Tool Availability

## Status

Proposed

## Date

2025-12-23

## Context

With the reconciliation framework implemented in PR #171 and session-scoped tool visibility introduced in [ADR 006](006-session-scoped-tool-visibility.md), we need to define:

1. **What state should be synced** from muster's runtime back to Kubernetes CRs
2. **How tool availability works** with session-based visibility
3. **Which status fields are obsolete** and should be removed or repurposed

### Current CRD Status Fields

The existing CRD definitions include status fields designed for a **global tool registry** model:

**MCPServer:**
```yaml
status:
  state: running              # Runtime state
  health: healthy             # Health status
  availableTools: [...]       # Tools provided by this server
  lastError: ""               # Error message
  conditions: []              # K8s conditions
```

**ServiceClass:**
```yaml
status:
  available: true             # All required tools available?
  requiredTools: [...]        # Tools needed
  missingTools: [...]         # Tools not found
  toolAvailability: {...}     # Per-lifecycle-tool status
  conditions: []
```

**Workflow:**
```yaml
status:
  available: true             # All required tools available?
  requiredTools: [...]        # Tools needed
  missingTools: [...]         # Tools not found
  stepValidation: [...]       # Per-step validation
  conditions: []
```

### The Problem with Global Tool Availability

With session-scoped visibility (ADR 006), **tool availability is no longer global**:

```
Session A (authenticated with mcp-kubernetes):
  Available tools: core_*, kubernetes_*

Session B (not authenticated):
  Available tools: core_*, authenticate_mcp_kubernetes

Session C (authenticated with mcp-github):
  Available tools: core_*, github_*
```

This means:
- A Workflow requiring `kubernetes_list_pods` is **available to Session A** but **not available to Session B**
- A ServiceClass requiring `github_create_pr` is **available to Session C** but **not to Sessions A or B**
- The same CR has different "availability" depending on who's asking

**Writing `available: true/false` to the CR status is meaningless** because:
1. It would constantly flip-flop as different sessions query it
2. It represents no single user's view
3. It creates confusion about whether a workflow/serviceclass can be used

### What Actually Makes Sense

For CRD status, we should focus on **objective, session-independent state**:

| CRD | Objective State | Session-Dependent State (don't sync) |
|-----|-----------------|--------------------------------------|
| **MCPServer** | Process state, health, errors | AvailableTools (depends on server + user permissions) |
| **ServiceClass** | Validation errors, spec issues | Tool availability (session-dependent) |
| **Workflow** | Validation errors, spec issues | Tool availability (session-dependent) |

## Decision

### 1. MCPServer Status: Sync Runtime State

MCPServer CRs represent actual running processes/connections. Their status should reflect **objective runtime state**:

```yaml
status:
  # KEEP: Objective runtime state
  state: running          # unknown|starting|running|stopping|stopped|failed
  health: healthy         # unknown|healthy|unhealthy|checking
  lastError: ""           # Error from recent operations
  conditions: []          # K8s standard conditions
  
  # NEW: Connection metadata
  lastConnected: "2025-12-23T10:00:00Z"   # When muster last connected
  restartCount: 0                          # How many times restarted
  
  # REMOVE: Session-dependent data
  # availableTools: []    # Depends on user permissions at OAuth server
```

**Sync triggers:**
- MCP server process starts/stops → update `state`
- Health check completes → update `health`
- Connection error occurs → update `lastError`
- Process restart → increment `restartCount`

### 2. ServiceClass Status: Validation Only

ServiceClass CRs are templates. Their status should reflect **definition validity**, not runtime tool availability:

```yaml
status:
  # KEEP: Objective validation state
  valid: true                    # Spec passes structural validation
  validationErrors: []           # Any spec validation errors
  conditions: []                 # K8s standard conditions
  
  # NEW: Extracted tool references (informational only)
  referencedTools:               # Tools mentioned in spec (for documentation)
    - kubernetes_apply
    - kubernetes_delete
  
  # REMOVE: Session-dependent availability
  # available: true              # Meaningless with session visibility
  # requiredTools: []            # Redundant with referencedTools
  # missingTools: []             # Session-dependent
  # toolAvailability: {}         # Session-dependent
```

**Sync triggers:**
- CR created/updated → validate spec, update `valid` and `validationErrors`
- Extract tool references from lifecycle tools → update `referencedTools`

### 3. Workflow Status: Validation Only

Workflow CRs are execution templates. Their status should reflect **definition validity**:

```yaml
status:
  # KEEP: Objective validation state
  valid: true                    # Spec passes structural validation
  validationErrors: []           # Any spec validation errors
  conditions: []                 # K8s standard conditions
  
  # NEW: Extracted tool references (informational only)
  referencedTools:               # Tools mentioned in steps (for documentation)
    - docker_build
    - kubernetes_deploy
  stepCount: 5                   # Number of steps (quick reference)
  
  # REMOVE: Session-dependent availability
  # available: true              # Meaningless with session visibility
  # requiredTools: []            # Redundant with referencedTools
  # missingTools: []             # Session-dependent
  # stepValidation: []           # Tool availability is session-dependent
```

**Sync triggers:**
- CR created/updated → validate spec, update `valid` and `validationErrors`
- Extract tool references from steps → update `referencedTools`

### 4. Session-Aware Tool Availability at Runtime

Tool availability is computed **per-session at runtime**, not stored in CRs:

```
User calls: tools/list
  └── Returns tools available to THIS session

User calls: workflow_list
  └── Each workflow includes computed "canExecute" based on session's tools

User calls: workflow_run deploy-app
  └── If session lacks required tools:
      └── Error: "Missing tools: kubernetes_deploy. Authenticate with mcp-kubernetes first."
```

#### API Response Enhancement

The list tools should return availability information per-session:

```go
// For tools/list response
type WorkflowInfo struct {
    Name            string   `json:"name"`
    Description     string   `json:"description"`
    ReferencedTools []string `json:"referencedTools"`  // Tools the workflow uses
    CanExecute      bool     `json:"canExecute"`       // True if session has all tools
    MissingTools    []string `json:"missingTools"`     // Tools session lacks
}
```

This is computed dynamically:
```go
func (a *Aggregator) GetWorkflowInfo(ctx context.Context, workflow *Workflow) WorkflowInfo {
    sessionID := getSessionID(ctx)
    sessionTools := a.GetToolsForSession(sessionID)
    
    missing := []string{}
    for _, tool := range workflow.ReferencedTools {
        if !sessionTools.Has(tool) {
            missing = append(missing, tool)
        }
    }
    
    return WorkflowInfo{
        Name:            workflow.Name,
        ReferencedTools: workflow.ReferencedTools,
        CanExecute:      len(missing) == 0,
        MissingTools:    missing,
    }
}
```

### 5. Implementation: MusterClient Status Updates

Add status update methods to the `MusterClient` interface:

```go
type MusterClient interface {
    // Existing methods...
    
    // Status update methods (use Status subresource)
    UpdateMCPServerStatus(ctx context.Context, server *MCPServer) error
    UpdateServiceClassStatus(ctx context.Context, sc *ServiceClass) error
    UpdateWorkflowStatus(ctx context.Context, wf *Workflow) error
}
```

Implementation for Kubernetes mode:
```go
func (k *kubernetesClient) UpdateMCPServerStatus(ctx context.Context, server *MCPServer) error {
    return k.Client.Status().Update(ctx, server)
}
```

For filesystem mode, status is stored in a separate `.status.yaml` file or embedded in the main file.

### 6. Implementation: Reconciler Status Sync

Update reconcilers to sync status after operations:

```go
func (r *MCPServerReconciler) Reconcile(ctx context.Context, req ReconcileRequest) ReconcileResult {
    // ... existing reconciliation logic ...
    
    // After successful start/stop/restart, sync status back to CR
    if r.client.IsKubernetesMode() {
        server, err := r.client.GetMCPServer(ctx, req.Name, req.Namespace)
        if err != nil {
            return ReconcileResult{Error: err, Requeue: true}
        }
        
        // Get actual runtime state
        service, exists := r.serviceRegistry.Get(req.Name)
        if exists {
            server.Status.State = string(service.GetState())
            server.Status.Health = string(service.GetHealth())
            server.Status.LastError = service.GetError()
        } else {
            server.Status.State = "stopped"
            server.Status.Health = "unknown"
        }
        
        if err := r.client.UpdateMCPServerStatus(ctx, server); err != nil {
            logging.Warn("MCPServerReconciler", "Failed to update status: %v", err)
        }
    }
    
    return ReconcileResult{}
}
```

### 7. State Change Callback Integration

Subscribe to service state changes to sync status in real-time:

```go
func (r *MCPServerReconciler) SetupStateCallback(registry api.ServiceRegistryHandler) {
    registry.OnStateChange(func(name string, oldState, newState ServiceState, health HealthStatus, err error) {
        // Trigger status sync for this MCPServer
        go r.syncStatus(context.Background(), name)
    })
}
```

### 8. CRD Type Changes

Update the CRD types in `pkg/apis/muster/v1alpha1/`:

**MCPServerStatus (updated):**
```go
type MCPServerStatus struct {
    State         string             `json:"state,omitempty"`
    Health        string             `json:"health,omitempty"`
    LastError     string             `json:"lastError,omitempty"`
    LastConnected *metav1.Time       `json:"lastConnected,omitempty"`
    RestartCount  int                `json:"restartCount,omitempty"`
    Conditions    []metav1.Condition `json:"conditions,omitempty"`
    // Removed: AvailableTools []string
}
```

**ServiceClassStatus (updated):**
```go
type ServiceClassStatus struct {
    Valid            bool               `json:"valid,omitempty"`
    ValidationErrors []string           `json:"validationErrors,omitempty"`
    ReferencedTools  []string           `json:"referencedTools,omitempty"`
    Conditions       []metav1.Condition `json:"conditions,omitempty"`
    // Removed: Available, RequiredTools, MissingTools, ToolAvailability
}
```

**WorkflowStatus (updated):**
```go
type WorkflowStatus struct {
    Valid            bool               `json:"valid,omitempty"`
    ValidationErrors []string           `json:"validationErrors,omitempty"`
    ReferencedTools  []string           `json:"referencedTools,omitempty"`
    StepCount        int                `json:"stepCount,omitempty"`
    Conditions       []metav1.Condition `json:"conditions,omitempty"`
    // Removed: Available, RequiredTools, MissingTools, StepValidation
}
```

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          Kubernetes Cluster                              │
│                                                                          │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐       │
│  │ MCPServer CR     │  │ ServiceClass CR  │  │ Workflow CR      │       │
│  │                  │  │                  │  │                  │       │
│  │ status:          │  │ status:          │  │ status:          │       │
│  │   state: running │  │   valid: true    │  │   valid: true    │       │
│  │   health: healthy│  │   referencedTools│  │   referencedTools│       │
│  │   lastError: ""  │  │     - tool_a     │  │     - tool_x     │       │
│  └────────┬─────────┘  │     - tool_b     │  │     - tool_y     │       │
│           │            └──────────────────┘  └──────────────────┘       │
│           │                                                              │
└───────────┼──────────────────────────────────────────────────────────────┘
            │ Status Updates
            ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                           Muster Server                                  │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                    Reconciliation Manager                           │ │
│  │                                                                     │ │
│  │  ┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐       │ │
│  │  │ MCPServer       │ │ ServiceClass    │ │ Workflow        │       │ │
│  │  │ Reconciler      │ │ Reconciler      │ │ Reconciler      │       │ │
│  │  │                 │ │                 │ │                 │       │ │
│  │  │ Syncs: state,   │ │ Syncs: valid,   │ │ Syncs: valid,   │       │ │
│  │  │ health, error   │ │ referencedTools │ │ referencedTools │       │ │
│  │  └────────┬────────┘ └─────────────────┘ └─────────────────┘       │ │
│  └───────────┼─────────────────────────────────────────────────────────┘ │
│              │                                                           │
│              ▼                                                           │
│  ┌─────────────────────────────────────────────────────────────────────┐│
│  │                    Session Manager                                   ││
│  │                                                                      ││
│  │  Session A (User A)              Session B (User B)                  ││
│  │  ┌────────────────────┐         ┌────────────────────┐              ││
│  │  │ Authenticated:     │         │ Authenticated:     │              ││
│  │  │   mcp-kubernetes   │         │   mcp-github       │              ││
│  │  │                    │         │                    │              ││
│  │  │ Available Tools:   │         │ Available Tools:   │              ││
│  │  │   - core_*         │         │   - core_*         │              ││
│  │  │   - kubernetes_*   │         │   - github_*       │              ││
│  │  │                    │         │                    │              ││
│  │  │ Workflow "deploy": │         │ Workflow "deploy": │              ││
│  │  │   canExecute: true │         │   canExecute: false│              ││
│  │  │   missing: []      │         │   missing: [k8s_*] │              ││
│  │  └────────────────────┘         └────────────────────┘              ││
│  └──────────────────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────────────────┘
```

## Consequences

### Benefits

1. **Clear separation of concerns**: CRs store objective state; runtime computes session-specific availability
2. **Meaningful status**: `kubectl get mcpservers` shows actual runtime state, not confusing global availability
3. **No status thrashing**: Status updates are triggered by real state changes, not session activity
4. **Session-aware UX**: Users see accurate information about what they can execute
5. **Simpler CRDs**: Fewer fields, clearer semantics

### Costs

1. **API response computation**: Each list request computes per-session availability
2. **CRD migration**: Existing CRs with old status fields need migration
3. **Documentation updates**: Need to update CRD reference docs

### Migration Path

1. **Phase 1**: Add new status fields alongside old ones (backward compatible)
2. **Phase 2**: Update reconcilers to populate new fields
3. **Phase 3**: Deprecate old fields in documentation
4. **Phase 4**: Remove old fields in v1beta1 API version

## Implementation Checklist

- [ ] Update `MCPServerStatus` type in `pkg/apis/muster/v1alpha1/`
- [ ] Update `ServiceClassStatus` type
- [ ] Update `WorkflowStatus` type
- [ ] Add `UpdateMCPServerStatus()` to `MusterClient` interface
- [ ] Add `UpdateServiceClassStatus()` to `MusterClient` interface
- [ ] Add `UpdateWorkflowStatus()` to `MusterClient` interface
- [ ] Implement status methods in `kubernetesClient`
- [ ] Implement status methods in `filesystemClient`
- [ ] Update `MCPServerReconciler` to sync status
- [ ] Update `ServiceClassReconciler` to sync status
- [ ] Update `WorkflowReconciler` to sync status
- [ ] Add state change callbacks for real-time sync
- [ ] Update CRD YAML manifests
- [ ] Regenerate CRD with controller-gen
- [ ] Update documentation in `docs/reference/crds.md`
- [ ] Add tests for status sync
- [ ] Add migration notes for existing deployments

## Related ADRs

- [ADR 006: Session-Scoped Tool Visibility](006-session-scoped-tool-visibility.md) - Why tool availability is per-session
- [ADR 003: Configuration Management](003-configuration-management.md) - Filesystem vs Kubernetes modes

