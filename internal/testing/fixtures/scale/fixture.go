package scale

import _ "embed"

//go:generate go run ./gen

// PreConfigurationYAML is the fixture rendered as the harness's
// pre_configuration fragment (RenderPreConfiguration), what
// `pre_configuration.fixture: scale` loads.
//
//go:embed pre_configuration.yaml
var PreConfigurationYAML []byte

// SessionsYAML is the fixture's session records (RenderSessions).
//
//go:embed sessions.yaml
var SessionsYAML []byte
