// Package nativeconfig declares hand-rolled Go structs for the subset of the
// agentgateway native YAML configuration schema this repo emits.
//
// Source of truth:
//
//	https://raw.githubusercontent.com/agentgateway/agentgateway/refs/tags/v1.2.1/schema/config.json
//
// Codegen via github.com/atombender/go-jsonschema was attempted and rejected:
// the agw schema's anyOf usage (around AzureContentSafetyPolicies) trips the
// tool, and the schema covers far more surface than muster's emitter touches.
// These structs cover only the fields the YAML applier produces. Tags use
// yaml.v3 conventions; struct field order is significant for deterministic
// marshalling.
package nativeconfig
