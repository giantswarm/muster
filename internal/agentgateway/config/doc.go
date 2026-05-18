// Package config declares hand-rolled Go structs for the subset of the
// agentgateway native YAML configuration schema (v1.2.1) that muster emits.
//
// Source of truth:
//
//	https://raw.githubusercontent.com/agentgateway/agentgateway/refs/tags/v1.2.1/schema/config.json
//
// agentgateway is implemented in Rust and ships no Go bindings; these
// structs cover only the fields muster's YAML emitter produces. Tags use
// yaml.v3 conventions; struct field order is significant for deterministic
// marshalling.
package config
