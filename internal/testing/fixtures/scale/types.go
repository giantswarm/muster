package scale

// Name is the fixture's name in pre_configuration.fixture.
const Name = "scale"

// Fixture is the typed fixture: what the generator produces and what the
// budgets in internal/aggregator run on. The harness reads the same shape
// from its YAML rendering.
type Fixture struct {
	// Servers are the MCPServer definitions, in-house servers last.
	Servers []Server
	// Documents are the distinct capability documents (tool lists) the
	// session-authenticated servers offer; a family member points at one by
	// name. Sessions reference them, never copy them.
	Documents []Document
	// Workflows reference the servers' tools.
	Workflows []Workflow
	// Sessions are the session records: every session is authenticated to
	// every session-authenticated server.
	Sessions []Session

	// MusterOAuthServer names the mock authorization server that is muster's
	// own; FleetIssuer the one whose tokens the forwardToken servers trust
	// (the tokens a scenario mints to open a session).
	MusterOAuthServer, FleetIssuer string
}

// Server is one MCPServer definition.
type Server struct {
	Name string
	// Installation the server runs on; empty for an in-house server.
	Installation string
	// Family and InstanceArg group the server with its siblings on the other
	// installations; empty for an in-house server.
	Family, InstanceArg string
	// Document names the capability document the server offers.
	Document string
	// SessionAuth: the server requires per-session authentication (a
	// forwardToken server here); false for an in-house server whose tools
	// every session sees.
	SessionAuth bool
}

// URL is the server's invented endpoint, for the in-process budgets (the
// harness gives every server a mock's address instead).
func (s Server) URL() string {
	return "https://" + s.Name + ".mcp.fixture.invalid/mcp"
}

// Document is one capability document: the tools a server offers.
type Document struct {
	Name  string
	Tools []Tool
}

// Tool is one tool of a document.
type Tool struct {
	Name        string
	Description string
	// Properties are the tool's input schema properties, name -> description;
	// every property is a string.
	Properties []Property
	// Required lists the required properties.
	Required []string
	// ReadOnly is the tool's readOnlyHint annotation.
	ReadOnly bool
}

// Property is one input schema property.
type Property struct {
	Name, Description string
}

// Workflow is one workflow definition.
type Workflow struct {
	Name        string
	Description string
	Labels      map[string]string
	// Args are the workflow's arguments in declaration order.
	Args  []Arg
	Steps []Step
}

// Arg is one workflow argument.
type Arg struct {
	Name, Type, Description string
	Required                bool
}

// Step is one workflow step.
type Step struct {
	ID   string
	Tool string
	// Args are the step's arguments in declaration order (values may be
	// templates over the workflow's input).
	Args []StepArg
}

// StepArg is one step argument.
type StepArg struct {
	Name, Value string
}

// Session is one session record.
type Session struct {
	ID      string
	Subject string
}

// SessionAuthServers returns the servers that require per-session
// authentication -- the ones every session of the fixture is authenticated to.
func (f *Fixture) SessionAuthServers() []Server {
	var out []Server
	for _, s := range f.Servers {
		if s.SessionAuth {
			out = append(out, s)
		}
	}
	return out
}

// DocumentByName returns the named document.
func (f *Fixture) DocumentByName(name string) (Document, bool) {
	for _, d := range f.Documents {
		if d.Name == name {
			return d, true
		}
	}
	return Document{}, false
}

// Shape is the fixture's headline numbers, for reports and assertions.
type Shape struct {
	Servers, SessionAuthServers, Families, Installations int
	// Documents counts every distinct tool list; SessionDocuments the ones
	// the session-authenticated servers offer -- what the capability store
	// holds, once each, for every session.
	Documents, SessionDocuments int
	Workflows, Sessions         int
}

// Shape returns the fixture's headline numbers.
func (f *Fixture) Shape() Shape {
	families := map[string]struct{}{}
	installations := map[string]struct{}{}
	sessionAuth := 0
	sessionDocs := map[string]struct{}{}
	for _, s := range f.Servers {
		if s.Family != "" {
			families[s.Family] = struct{}{}
		}
		if s.Installation != "" {
			installations[s.Installation] = struct{}{}
		}
		if s.SessionAuth {
			sessionAuth++
			sessionDocs[s.Document] = struct{}{}
		}
	}
	return Shape{
		Servers:            len(f.Servers),
		SessionAuthServers: sessionAuth,
		Families:           len(families),
		Installations:      len(installations),
		Documents:          len(f.Documents),
		SessionDocuments:   len(sessionDocs),
		Workflows:          len(f.Workflows),
		Sessions:           len(f.Sessions),
	}
}

// exposedToolName is the name a tool of the server carries in a session's
// catalogue: x_<family>_<tool> for a family member, x_<server>_<tool>
// otherwise.
func exposedToolName(s Server, tool string) string {
	if s.Family != "" {
		return "x_" + s.Family + "_" + tool
	}
	return "x_" + s.Name + "_" + tool
}

// ExposedToolName is exposedToolName for callers outside the package.
func ExposedToolName(s Server, tool string) string { return exposedToolName(s, tool) }
