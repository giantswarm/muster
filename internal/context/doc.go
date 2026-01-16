// Package context provides kubectl-style context management for muster CLI.
//
// This package enables users to manage multiple muster endpoints similar to
// how kubectl handles kubeconfig contexts. Users can define named contexts
// pointing to different muster aggregator servers and switch between them
// without needing to specify --endpoint for every command.
//
// # Configuration File
//
// Contexts are stored in ~/.config/muster/contexts.yaml with the following schema:
//
//	current-context: production
//	contexts:
//	  - name: local
//	    endpoint: http://localhost:8090/mcp
//	  - name: production
//	    endpoint: https://muster.example.com/mcp
//	    settings:
//	      output: table
//
// # Usage
//
// The package provides CRUD operations for contexts:
//   - Add/update contexts with AddContext
//   - Remove contexts with DeleteContext
//   - List contexts with ListContexts
//   - Get current context with GetCurrentContext
//   - Switch contexts with SetCurrentContext
//
// # Precedence
//
// When determining which endpoint to use, muster checks in this order:
//  1. --endpoint flag (highest priority)
//  2. --context flag
//  3. MUSTER_CONTEXT environment variable
//  4. current-context from contexts.yaml
//  5. Local fallback (http://localhost:8090/mcp)
//
// # Concurrency
//
// Storage operations are thread-safe within a single process using a read-write
// mutex. However, concurrent access from multiple processes (e.g., running
// multiple muster commands simultaneously) is not protected. In practice, this
// is rarely an issue as context modifications are infrequent user-initiated
// actions, but users should avoid running concurrent context add/delete/rename
// operations.
package context
