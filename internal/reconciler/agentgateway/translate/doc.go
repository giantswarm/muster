// Package translate holds pure mappers from internal/api MCPServer types to
// the muster CRD types in pkg/apis/muster/v1alpha1. The reconciler imports
// this package to keep its orchestration code free of field-by-field copy
// logic.
package translate
