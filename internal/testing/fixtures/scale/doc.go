// Package scale is the installation-shaped fixture the scale budgets run on:
// 87 MCPServers (84 of them session-authenticated, in five families with an
// instanceArg, on 28 installations; three in-house servers without
// authentication), 18 distinct capability documents shared by the family
// members, 282 workflows referencing their tools, and 450 sessions each
// authenticated to every session-authenticated server -- the shape of a
// production installation, with every name invented.
//
// The generator is the source of truth (Fixture); the committed YAML files
// are its rendering for the test harness (pre_configuration.yaml, loaded by
// `pre_configuration.fixture: scale`) and for review (sessions.yaml).
// `go generate ./internal/testing/fixtures/scale/` rewrites both; a test
// fails when they drift from the generator.
//
// Nothing in the fixture comes from an installation: installations are named
// after minerals, hostnames end in .invalid, tool names are generic verbs
// over the family's nouns, workflow names are verb-object-qualifier
// combinations, people are numbered.
package scale
