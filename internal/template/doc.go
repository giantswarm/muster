// Package template implements the variable substitution and templating
// engine used by workflow steps and ServiceClass tool calls.
//
// The engine resolves Go-style `{{ ... }}` expressions against an execution
// context that exposes workflow inputs (`{{ .input.* }}`) and prior step
// results (`{{ steps.<id>.* }}`). It supports both simple variable access
// (returning the underlying typed value) and full text-template rendering
// (returning a string), with a dedicated path for each so that argument
// passing preserves types.
package template
