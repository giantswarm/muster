# JSON Schema 2020-12 for Tools (MCP 2026-07-28)

The MCP `2026-07-28` release candidate makes two related changes that
together rewire how tools are described and how one specific failure
mode is reported on the wire:

- **[SEP-2106](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2106) — "Tools `inputSchema` & `outputSchema` Conform to JSON Schema 2020-12"** lifts the long-standing restriction that tool schemas may only use `type`, `properties` and `required`. From this revision on, `inputSchema` keeps `type: "object"` at the root but accepts every other JSON Schema 2020-12 keyword (composition, conditionals, references, etc.); `outputSchema` loses the `type: "object"` constraint entirely and may be any valid JSON Schema; and the `structuredContent` field of `CallToolResult` is widened from `{ [key: string]: unknown }` to plain `unknown` so a tool result can be an array, primitive, `null`, or object.
- **[SEP-2164](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2164) — "Standardize resource not found error code (-32602)"** changes the JSON-RPC error code recommended for "resource not found" from MCP's custom `-32002` to the standard JSON-RPC `-32602` (Invalid Params). Clients SHOULD continue to accept `-32002` during the transition; servers MUST emit `-32602`. Empty `contents` arrays for non-existent resources are now explicitly forbidden.

The announcement frames both as one item under "Full JSON Schema 2020-12
for Tools"
([blog post](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)):

> Tool `inputSchema` and `outputSchema` are lifted to full JSON Schema
> 2020-12 (SEP-2106). Input schemas keep the `type: "object"` root
> constraint but now allow composition (`oneOf`, `anyOf`, `allOf`),
> conditionals, and references (`$ref`, `$defs`). Output schemas are
> unrestricted, and `structuredContent` can now be any JSON value
> rather than only an object. Implementations must not auto-dereference
> external `$ref` URIs and should bound schema depth and validation
> time. Separately, the error code for a missing resource changes from
> the MCP-custom `-32002` to the JSON-RPC standard `-32602` Invalid
> Params (SEP-2164). If your client matches on the literal `-32002`
> value, update it.

For muster the punch line is that both changes are **largely
opportunistic for the aggregator and only mildly invasive for the
muster-as-server surface**. The wire format is backward compatible:
muster's existing schemas continue to pass through unchanged, and
muster does not currently emit the literal `-32002` JSON-RPC error
code in any Go source file (§3 below). The work is mainly about
**not regressing** on schemas richer than the old subset when they
flow through muster's aggregator, **policing** the new security
implications of full JSON Schema 2020-12 (`$ref` SSRF and
composition-DoS), and **broadening** the test surface so structured
tool results that are arrays or primitives are not silently rewrapped
into objects.

## 1. What the spec says

### 1.1 SEP-2106: tool schemas become full JSON Schema 2020-12

[SEP-2106](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2106)
was authored by John McBride and shepherded through the new SEP-1850
PR workflow by Ola Hungerford; the PR is `MERGED` against the `draft`
branch and carries both the `SEP` and `accepted` labels. The PR body
opens with the headline change:

> - **`inputSchema`**: Keep `type: "object"` required (since tool
>   arguments are objects), but allow any additional JSON Schema
>   2020-12 keywords — composition (`oneOf`/`anyOf`/`allOf`/`not`),
>   conditional (`if`/`then`/`else`), reference (`$ref`/`$defs`/
>   `$anchor`), etc.
> - **`outputSchema`**: Fully support JSON Schema 2020-12 (arrays,
>   primitives, objects, compositions)
> - **`structuredContent`**: Accept any JSON value validated by
>   `outputSchema`

The PR diff (`gh pr diff 2106 --repo modelcontextprotocol/modelcontextprotocol`)
delivers the change in three files:

- `schema/draft/schema.json` widens `Tool.inputSchema` and
  `Tool.outputSchema` to `additionalProperties: {}` with only
  `$schema` (and `type` on `inputSchema`) constrained, and drops the
  `additionalProperties: {}` + `type: object` constraint from
  `CallToolResult.structuredContent`, making it simply "an optional
  JSON value … that conforms to the tool's outputSchema if one is
  defined."
- `docs/specification/draft/schema.mdx` adds rendered docs for the
  new typings: `inputSchema: { $schema?: string; type: "object"; [key: string]: unknown }`
  and `outputSchema?: { $schema?: string; [key: string]: unknown }`,
  and adds the prose paragraph that names every keyword class
  (composition / conditional / reference / "any other standard
  validation or annotation keywords") that is now legal alongside
  `type`.
- `docs/specification/draft/server/tools.mdx` changes the body of the
  spec from "Structured content is returned as a JSON object" to
  "Structured content is returned as a JSON value … This can be any
  JSON value (object, array, string, number, boolean, or null) that
  conforms to the tool's `outputSchema` if one is defined", and adds
  two new example blocks: one tool with an array `outputSchema` and a
  matching response carrying a top-level `structuredContent: [ … ]`,
  and one tool whose `inputSchema` uses `oneOf` for id-or-name lookup.

The PR also ships three schema example fixtures
(`schema/draft/examples/CallToolResult/result-with-array-structured-content.json`,
`schema/draft/examples/Tool/tool-with-array-output-schema.json`,
`schema/draft/examples/Tool/tool-with-composition-input-schema.json`)
that the conformance suite can pick up directly.

#### Motivation in the SEP's own words

The PR body is unusually explicit about why the old subset was a
problem:

> **The Problem:** A weather API tool that returns hourly forecasts
> cannot return the natural response:
> ```json
> [
>   { "hour": "09:00", "temp": 68, "conditions": "sunny" },
>   { "hour": "10:00", "temp": 72, "conditions": "partly cloudy" }
> ]
> ```
> Instead, developers must wrap arrays in unnecessary container
> objects — conflicting with common REST API patterns (GitHub Events
> API, AccuWeather, standard collection endpoints).
>
> **SDK Evidence:** This isn't theoretical friction. FastMCP, one of
> the most popular Python SDKs, has implemented extensive workarounds
> with error messages that explicitly cite "MCP spec limitations."
> These workarounds have caused real bugs: Issue #2455: `$ref`
> schemas without `type: object` broke ALL tools on a server. Issue
> #2421: Unexpected `{"result": ...}` wrapping confused users.
>
> **Industry Precedent:** OpenAPI went through this same evolution.
> OpenAPI 3.0's "extended subset" of JSON Schema caused years of
> tooling confusion. OpenAPI 3.1 made the strategic decision to fully
> align with JSON Schema 2020-12 — MCP can learn from this rather
> than repeating the same journey.

The "Rationale" section of the SEP page (added in the PR diff at
`docs/seps/2106-json-schema-2020-12.mdx`) underlines that the spec was
already claiming JSON Schema support but only honouring a tiny subset
("the specification's claim of JSON Schema support") and that
rejecting array `structuredContent` was preventing a large class of
REST-aligned API designs.

#### Backward compatibility — wire-compatible, but asymmetric

The PR body publishes a directional compatibility matrix that the
spec text adopts almost verbatim:

|                              | New client (post-SEP) | Old client (pre-SEP)                                                                              |
| ---------------------------- | --------------------- | ------------------------------------------------------------------------------------------------- |
| **New server (post-SEP)**    | Fully compatible      | Compatible **only** for object-typed `structuredContent`. Arrays/primitives may break old clients |
| **Old server (pre-SEP)**     | Fully compatible      | Unchanged                                                                                         |

The single rule the SEP imposes to soften the asymmetric case is:

> To stay interoperable with old clients, servers using array or
> primitive `structuredContent` MUST also emit a `TextContent` block
> with the serialized JSON.

The SEP also calls out a **source-breaking** change for typed SDKs:
widening `structuredContent` from `{ [key: string]: unknown }` to
`unknown` removes property access without narrowing guards, and the
spec recommends SDK maintainers provide typed helpers keyed off
`outputSchema` rather than make consumers write `typeof sc === "object"`
checks by hand.

#### Security implications — `$ref` SSRF and composition-DoS

This is the part of SEP-2106 that creates the most genuinely new work
for any aggregator, including muster. The PR body is the canonical
source:

> **`$ref` SSRF:** Implementations MUST NOT automatically dereference
> `$ref` URIs that aren't same-document JSON Pointers (e.g.
> `#/$defs/Foo`). Opt-in modes for fetching external `$ref`s MUST
> default off and SHOULD enforce host allowlists, timeouts, size
> limits, and reject loopback/link-local/private addresses.
>
> **Composition cost:** Implementations SHOULD bound schema depth,
> total subschemas, and per-validation time to prevent a malicious
> tool definition from acting as a CPU DoS vector.

The full prose lives in the SEP page added by the PR
(`docs/seps/2106-json-schema-2020-12.mdx`) under "Security Implications
→ `$ref` Dereferencing (SSRF and Fetch-DoS)" and "→ Composition-Keyword
Resource Use" and adds two specifics that muster MUST implement
(re-read these as MUST text, not summary):

- "Implementations MUST NOT automatically dereference `$ref` values
  that resolve to a network URI (i.e. anything that is not a
  same-document JSON Pointer such as `#/$defs/Foo` or an internal
  `$anchor`)."
- "Schemas that fail to validate due to an unresolved external `$ref`
  SHOULD be rejected rather than silently treated as permissive."

The composition-DoS guidance is SHOULD-level but is the same
shape: bound depth, total subschemas, per-validation time budget.

#### What "JSON Schema 2020-12" means concretely

The new spec text references the standard at
[https://json-schema.org/draft/2020-12](https://json-schema.org/draft/2020-12).
The PR diff does not pin a `$schema` URI; instead the rendered schema
docs add a `$schema?: string` field to both `inputSchema` and
`outputSchema` and say:

> Defaults to JSON Schema 2020-12 when no explicit `$schema` is
> provided.

That makes 2020-12 the implicit dialect for everything muster emits or
forwards unless a tool explicitly opts into draft-07 or another
dialect (the spec keeps the draft-07 example block in
`tools.mdx`). For muster, the practical impact is that any new code
that **constructs** a schema in muster's own provider layer should
target 2020-12 keyword semantics (e.g. `prefixItems` rather than the
ambiguous tuple form of `items`, `unevaluatedProperties` rather than
post-`additionalProperties` workarounds) — but muster does not need
to ship a JSON Schema 2020-12 validator just to forward schemas
produced by upstream MCP servers.

#### Draft tools spec — what muster's `tools/list` output must now allow

The draft tools page
([https://modelcontextprotocol.io/specification/draft/server/tools](https://modelcontextprotocol.io/specification/draft/server/tools))
incorporates the PR's prose change and is the canonical reference for
the new wire-level contract:

> **Structured** content is returned as a JSON value in the
> `structuredContent` field of a result. This can be any JSON value
> (object, array, string, number, boolean, or null) that conforms to
> the tool's `outputSchema` if one is defined.
>
> For backwards compatibility, a tool that returns structured content
> SHOULD also return the serialized JSON in a TextContent block.

The same page gains the array-output example used in the SEP diff:

```json
{
  "name": "list_users",
  "title": "User List",
  "description": "Returns a list of all users",
  "inputSchema": {
    "type": "object",
    "properties": {}
  },
  "outputSchema": {
    "type": "array",
    "items": {
      "type": "object",
      "properties": {
        "id": { "type": "string" },
        "name": { "type": "string" },
        "email": { "type": "string" }
      },
      "required": ["id", "name", "email"]
    }
  }
}
```

with a matching response whose `structuredContent` is a top-level
JSON array. Muster's aggregator MUST be able to forward that response
shape verbatim from an upstream MCP server through to a downstream
muster client without coercing it into an object.

### 1.2 SEP-2164: resource-not-found becomes `-32602`

[SEP-2164](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2164)
("Standardize Resource Not Found Error Code") was authored by Peter
Alexander and is `MERGED` against `draft` with the `SEP`, `final`, and
`accepted` labels. The PR body is two sentences:

> This SEP standardizes the error code for resource not found from
> `-32002` to `-32602` (Invalid Params), aligning with the JSON-RPC
> specification. Includes the draft spec change.

The PR diff updates three places:

- `docs/seps/2164-resource-not-found-error.mdx` adds the full SEP
  page, including a survey of the existing SDKs (four different error
  codes in use today: `-32002` in C#/Rust/Java/Go/PHP, `-32602` in
  TypeScript, `-32603` in Kotlin, `0` in Python; Ruby and Swift leave
  it unspecified). The "Specification" section is unambiguous:
  > If the requested resource does not exist, servers MUST return a
  > JSON-RPC error with code `-32602` (Invalid Params). … The `data`
  > field SHOULD include the `uri` that was not found. Servers MUST
  > NOT return an empty `contents` array for a non-existent resource.
  > An empty array is ambiguous — it could mean the resource exists
  > but has no content, or that it doesn't exist at all.

- `docs/specification/draft/server/resources.mdx` swaps `Resource not
  found: -32002` for `Resource not found: -32602 (Invalid Params)`,
  adds a backwards-compatibility paragraph:
  > For backwards compatibility, clients **SHOULD** also accept
  > `-32002` as a resource not found error, as earlier protocol
  > versions used this code.
  and re-asserts the no-empty-`contents` rule.

- `docs/specification/draft/changelog.mdx` records the change in the
  draft changelog:
  > 6. Change resource not found error code from `-32002` to `-32602`
  >    (Invalid Params) to align with JSON-RPC specification.

The rationale, again straight from the SEP page added by the PR:

> `-32602` is the standard JSON-RPC error code for invalid parameters.
> A non-existent URI is semantically an invalid parameter — the
> client provided a URI that doesn't correspond to any resource. This
> aligns with the TypeScript SDK's existing behavior and avoids
> introducing custom error codes outside the JSON-RPC reserved range.
> Custom codes in the `-32000` to `-32099` range are "reserved for
> implementation-defined server errors" per JSON-RPC spec, not for
> protocol-level semantics.

The migration path stated in the SEP is short: servers update to
`-32602`; clients SHOULD accept both `-32602` and `-32002` during the
transition; the spec documents `-32602` as canonical.

### 1.3 The two SEPs together: why this section bundles them

Both SEPs end up in the same announcement section because they share a
single muster-facing surface: muster's tool/resource layer. SEP-2106 is
about how a tool **declares its schema and structured result** and
SEP-2164 is about how the server reports a `resources/read` failure.
Both touch the same code paths in muster's aggregator (tool
registration / forwarding) and the same conformance scenarios. The
"Full JSON Schema 2020-12 for Tools" framing in the announcement also
calls them out together for that reason.

## 2. Linked SEPs and PRs

- [SEP-2106 — Tools inputSchema & outputSchema Conform to JSON Schema 2020-12](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2106)
- [SEP-2164 — Standardize Resource Not Found Error Code](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2164)
- [Draft tools spec (/specification/draft/server/tools)](https://modelcontextprotocol.io/specification/draft/server/tools)
- [Draft resources spec (/specification/draft/server/resources)](https://modelcontextprotocol.io/specification/draft/server/resources) (updated by SEP-2164's diff)
- [Draft changelog (records both changes)](https://modelcontextprotocol.io/specification/draft/changelog)
- [JSON Schema 2020-12](https://json-schema.org/draft/2020-12)
- Announcement section "Full JSON Schema 2020-12 for Tools":
  [release-candidate blog post](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)

## 3. Muster impact

The impact decomposes cleanly into four code areas plus one piece of
generated documentation: tool-schema construction, tool-schema
forwarding, structured-content forwarding, the `-32002` audit, and the
`schema.json` generator. Each is concrete, narrow, and citable.

### 3.1 Tool-schema construction is already permissive (the good news)

Muster does not own the JSON Schema validator; the on-the-wire schema
of any tool muster exposes is built from
[internal/api.ArgMetadata](../../../internal/api/types.go), whose
`Schema` field is a wide-open `map[string]interface{}`
([internal/api/types.go:121-149](../../../internal/api/types.go)):

```126:170:internal/aggregator/tool_factory.go
func convertToMCPSchema(params []api.ArgMetadata) mcp.ToolInputSchema {
	properties := make(map[string]interface{})
	required := []string{}

	for _, param := range params {
		var propSchema map[string]interface{}

		// Use detailed schema if available, otherwise fall back to basic type
		if len(param.Schema) > 0 {
			// Use the detailed schema definition
			propSchema = make(map[string]interface{})
			for key, value := range param.Schema {
				propSchema[key] = value
			}

			// Ensure description is included (override schema description if needed)
			if param.Description != "" {
				propSchema["description"] = param.Description
			}
		} else {
			// Fall back to basic type-based schema
			propSchema = map[string]interface{}{
				"type":        param.Type,
				"description": param.Description,
			}
		}

		// Add default value if specified
		if param.Default != nil {
			propSchema["default"] = param.Default
		}

		properties[param.Name] = propSchema

		if param.Required {
			required = append(required, param.Name)
		}
	}

	return mcp.ToolInputSchema{
		Type:       "object",
		Properties: properties,
		Required:   required,
	}
}
```

The salient detail for SEP-2106 is the `for key, value := range param.Schema` block:
arbitrary JSON Schema 2020-12 keywords already pass through. A
provider that wants to declare `oneOf` on a single argument's schema
can stuff it into `ArgMetadata.Schema` today and `convertToMCPSchema`
will copy it verbatim onto the property. The same is true for `$ref`
inside the property schema (which is what the SEP-2106 SSRF rule is
about), for `if`/`then`/`else`, and for `$defs` at the property level.

The wrapper that the aggregator returns is still hard-coded:

```165:170:internal/aggregator/tool_factory.go
	return mcp.ToolInputSchema{
		Type:       "object",
		Properties: properties,
		Required:   required,
	}
```

That is exactly what SEP-2106 still requires — `inputSchema` keeps
`type: "object"` at the root — but it also means muster has no way to
attach **top-level** composition keywords (`oneOf`/`anyOf`/`allOf`/
`if`/`then`/`else`) on the root, even though SEP-2106 explicitly
allows them alongside `type: "object"` (see the
[tool-with-composition-input-schema.json](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2106/files)
fixture in the SEP-2106 diff). The same is true for the hand-written
auth tools further down the same file:

```260:289:internal/aggregator/tool_factory.go
	authTools := []mcp.Tool{
		{
			Name:        corePrefix + "auth_login",
			Description: "Authenticate to an OAuth-protected MCP server",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"server": map[string]interface{}{
						"type":        "string",
						"description": "Name of the MCP server to authenticate to",
					},
				},
				Required: []string{"server"},
			},
		},
		{
			Name:        corePrefix + "auth_logout",
			Description: "Log out from an OAuth-protected MCP server",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"server": map[string]interface{}{
						"type":        "string",
						"description": "Name of the MCP server to log out from",
					},
				},
				Required: []string{"server"},
			},
		},
	}
```

These two auth tools (and any other future hand-rolled muster tool
that bypasses the `ArgMetadata` pipeline) are not gated by SEP-2106 in
any way — they are object schemas with no composition keywords — but
they are an example of muster's pattern: muster's own meta-tools
declare a tiny, well-typed schema and have no need of the new
expressiveness.

The `mcp.ToolInputSchema` struct used here is supplied by `mark3labs/mcp-go`
and is the only artefact whose internal shape limits what muster can
emit today; if mcp-go's struct is the same as the `2025-11-25` subset
(`type` / `properties` / `required`), the **muster** side will need
to either upgrade mcp-go to a version whose `Tool.inputSchema` is
`map[string]any` (the SEP-2106 shape), or extend
`convertToMCPSchema` to return a `map[string]any` and hand it to
`mcp.Tool` directly. This is a dependency question, not a muster
design question.

There is no `outputSchema` construction anywhere in muster's Go tree
today; a `case-insensitive` grep for `outputSchema` across
`internal/api/` and `internal/aggregator/tool_factory.go` returns
zero matches. Muster does not currently declare structured output for
its meta-tools or its `workflow_*` tools. Adopting SEP-2106 is
therefore a green-field addition: muster CAN add `outputSchema` (and
`structuredContent`) to its own workflow tools, and SHOULD do so for
workflows whose result is well-typed (e.g. a list of services), but
no migration is forced on existing tools.

The `internal/api/json_keys.go` constants file underscores how narrow
the muster schema vocabulary is today:

```1:38:internal/api/json_keys.go
package api

// JSON Schema specification keys used when building MCP/OpenAPI-style
// schemas as map[string]any. These are wire-format strings: renaming them
// would break the protocol.
const (
	SchemaKeyType                 = "type"
	SchemaKeyDescription          = "description"
	SchemaKeyProperties           = "properties"
	SchemaKeyItems                = "items"
	SchemaKeyRequired             = "required"
	SchemaKeyDefault              = "default"
	SchemaKeyEnum                 = "enum"
	SchemaKeyAdditionalProperties = "additionalProperties"
)

// Field names used as keys when emitting muster API responses through
// untyped map[string]any (status payloads, tool results, mock responses).
const (
	FieldName        = "name"
	FieldStatus      = "status"
	FieldState       = "state"
	FieldHealth      = "health"
	FieldCommand     = "command"
	FieldArgs        = "args"
	FieldTools       = "tools"
	FieldSteps       = "steps"
	FieldError       = "error"
	FieldSuccess     = "success"
	FieldMessage     = "message"
	FieldServer      = "server"
	FieldMimeType    = "mimeType"
	FieldExecutionID = "execution_id"
	FieldInputSchema = "inputSchema"
	FieldURI         = "uri"
	FieldLabel       = "label"
	FieldID          = "id"
)
```

These are the only schema keys muster references by symbol; everything
else (composition keywords, `$ref`, `$defs`, `if`/`then`/`else`,
`prefixItems`, …) is plumbed via raw string keys in
`ArgMetadata.Schema` and survives unchanged through the aggregator.

### 3.2 The deprecated `WorkflowInputSchema` doesn't block SEP-2106 either

[internal/api/workflow.go:166-181](../../../internal/api/workflow.go)
defines a legacy `WorkflowInputSchema` struct:

```166:181:internal/api/workflow.go
// WorkflowInputSchema defines the input argument schema for a workflow.
// This provides structured validation and documentation for workflow inputs,
// following JSON Schema conventions for argument definition.
//
// DEPRECATED: Use Args map[string]ArgDefinition instead
type WorkflowInputSchema struct {
	// Type specifies the overall schema type (typically "object" for workflow inputs)
	Type string `yaml:"type" json:"type"`

	// Args defines the individual input arguments and their schemas.
	// Each property corresponds to a workflow input argument.
	Args map[string]SchemaProperty `yaml:"args" json:"args"`

	// Required lists the argument names that must be provided for workflow execution
	Required []string `yaml:"required,omitempty" json:"required,omitempty"`
}
```

It is already marked `DEPRECATED` in favour of the
`Args map[string]ArgDefinition` shape used by current workflows, and
`SchemaProperty`
([internal/api/types.go:307-319](../../../internal/api/types.go))
itself only carries `Type` / `Description` / `Default`. The SEP-2106
work is a useful **forcing function** to actually delete
`WorkflowInputSchema` (or, at minimum, hide it from any external
surface): keeping the deprecated path around encourages new code to
inherit the old subset.

### 3.3 Structured content forwarding (today: nothing to do; tomorrow: do not regress)

The aggregator currently coerces every tool result to text:

```296:328:internal/aggregator/tool_factory.go
// convertToMCPResult converts an internal tool result to MCP format.
//
// This function handles the conversion from the internal CallToolResult format
// to the MCP CallToolResult format. It processes different types of content:
//   - String content is converted directly to MCP text content
//   - Non-string content is marshaled to JSON and converted to text content
//   - Error status is preserved in the result
//
// This allows tools to return various types of data while ensuring compatibility
// with MCP clients that expect specific content formats.
//
// Args:
//   - result: Internal tool result from the tool provider
//
// Returns an MCP-compatible tool result with properly formatted content.
func convertToMCPResult(result *api.CallToolResult) *mcp.CallToolResult {
	mcpContent := make([]mcp.Content, len(result.Content))

	for i, content := range result.Content {
		if text, ok := content.(string); ok {
			mcpContent[i] = mcp.NewTextContent(text)
		} else {
			// Marshal non-string content to JSON for MCP compatibility
			jsonBytes, _ := json.Marshal(content)
			mcpContent[i] = mcp.NewTextContent(string(jsonBytes))
		}
	}

	return &mcp.CallToolResult{
		Content: mcpContent,
		IsError: result.IsError,
	}
}
```

Crucially: `convertToMCPResult` **never** sets
`mcp.CallToolResult.StructuredContent`. Muster's meta-tools and
workflow tools emit text only. That means muster does not currently
forward any non-object `structuredContent` shape on its **own**
tools, and the SEP-2106 widening from `{ [key: string]: unknown }` to
`unknown` is invisible to muster's own surface.

The forwarding path matters too. When muster's aggregator proxies a
`tools/call` to an upstream MCP server, it does so through the
mcp-go client; the upstream server's `structuredContent` is carried
back inside the `mcp.CallToolResult` that mcp-go returns. As long as
the mcp-go version muster pins permits non-object `structuredContent`
on its `CallToolResult` struct, no muster code change is required for
the forwarding path. If the pinned mcp-go version still types
`StructuredContent` as `map[string]any` (the old subset), an array or
primitive result from a SEP-2106-aware upstream will be lost on the
floor at the mcp-go boundary — that is a dependency upgrade item,
not a logic change in muster.

### 3.4 `-32002` literal audit: zero hits in muster's Go source

The plan asks specifically for a grep of the repo for literal
`-32002`. The only matches in the entire workspace are in the
documentation drafts written in this same series:

- `docs/explanation/mcp-2026-07-28/04-tasks-extension.md:278` (a
  prose mention of "legacy `-32002`; see SEP-2164" referenced from
  the Tasks doc).
- `docs/explanation/mcp-2026-07-28/04-tasks-extension.md:388` (the
  same doc's "References / migration notes" listing the
  `-32002` → `-32602` re-alignment).

There are **no Go-source uses** of `-32002` anywhere in
`internal/`, `pkg/`, or `cmd/`. The grep results for the
"resource not found" string clarify why: muster's resource-not-found
reporting is text-based and goes through `api.NotFoundError` /
`fmt.Errorf("resource not found: ...")`, never through a hard-coded
JSON-RPC error code:

- [internal/api/errors.go:118-127](../../../internal/api/errors.go)
  defines `NewResourceNotFoundError = func(name string) *NotFoundError`
  which formats `"resource <name> not found"` and carries no
  protocol-level code.
- [internal/metatools/handlers.go:355](../../../internal/metatools/handlers.go)
  returns `errorResult(fmt.Sprintf("Resource not found: %s", uri))`
  from `describe_resource`. The `errorResult` helper wraps the
  message in a `CallToolResult{IsError: true}`; it does not invoke a
  JSON-RPC error.
- [internal/metatools/handlers.go:379-381](../../../internal/metatools/handlers.go)
  (`handleGetResource`) returns `errorResult("Resource retrieval failed: %v", err)`
  on any error from `handler.GetResource(ctx, uri)`. Same shape.
- [cmd/get.go:347](../../../cmd/get.go) returns
  `fmt.Errorf("resource not found: %s", uri)` from the CLI's `get`
  path, which is a CLI exit-code-2 surface (see
  [docs/reference/cli/get.md](../../reference/cli/get.md)) and
  also not a JSON-RPC code.
- [internal/aggregator/server.go:3014-3030](../../../internal/aggregator/server.go)
  is muster's `ReadResource(ctx, uri)` implementation; the error
  return is whatever the upstream MCP client returns, again with no
  hard-coded JSON-RPC code in muster.

That means SEP-2164's "If your client matches on the literal `-32002`
value, update it" notice does **not** apply to any muster Go source.
However, three downstream surfaces emit `-32002` because they ride on
top of muster's resource-not-found logic:

- The CLI exit code documented as `2 | Resource not found` in
  [docs/reference/cli/check.md](../../reference/cli/check.md),
  [docs/reference/cli/get.md](../../reference/cli/get.md),
  [docs/reference/cli/start.md](../../reference/cli/start.md),
  [docs/reference/cli/README.md](../../reference/cli/README.md),
  and [docs/reference/README.md](../../reference/README.md) is an
  exit code, not a JSON-RPC error code, and is unaffected. (The
  number `2` here happens to collide visually with the last digit of
  `-32002`; this is coincidence.)
- The mcp-go library that muster pins is the one that actually emits
  the JSON-RPC error code on the wire when a `resources/read` fails.
  If that library still returns `-32002`, **muster's transitive
  on-the-wire behaviour is non-conforming** the moment the
  `2026-07-28` revision is final. Bumping mcp-go to a version that
  emits `-32602` for `resources/read` failures, with `data.uri`
  populated, is the entire fix.
- Any conformance scenario in
  [internal/testing/scenarios/](../../../internal/testing) that
  exercises `resources/read` on a non-existent URI MUST be updated
  to accept either `-32602` or `-32002` (the SEP says clients SHOULD
  accept both during the transition).

### 3.5 The "no empty `contents`" rule and muster's `get_resource`

SEP-2164 also adds a MUST that is independent of the error code:
> Servers MUST NOT return an empty `contents` array for a
> non-existent resource.

Muster's `handleGetResource` ([internal/metatools/handlers.go:368-394](../../../internal/metatools/handlers.go))
returns an error string when the upstream `handler.GetResource` fails,
not an empty contents array, so muster is already compliant on this
point. The risk is at the **upstream-forwarding** boundary
([internal/aggregator/server.go:3014-3030](../../../internal/aggregator/server.go)
`ReadResource`): if an upstream MCP server returns
`{"contents": []}` instead of an error, muster forwards that response
unchanged. SEP-2164 makes that upstream behaviour non-conforming, but
it does not give muster a reason to start synthesising errors on the
upstream's behalf; the right behaviour is to forward the upstream's
(incorrect) response and let the upstream owner fix it.

### 3.6 The `--generate-schema` generator and `schema.json`

The plan calls out
[docs/contributing/testing/api-schema-validation.md](../../contributing/testing/api-schema-validation.md)
and `cmd/test.go`'s `--generate-schema` flag (referenced in
[CLAUDE.md](../../../CLAUDE.md), the architecture rules, and
several how-to docs). The architecture rule (`architecture.mdc`)
states:

> **Don't change schema.json manually** It is generated via
> `muster test --generate-schema` and changes are the result of the
> actual api of `muster serve`.

For SEP-2106, the workflow is therefore:

1. Land any change that actually broadens muster's emitted schemas
   (e.g. allowing top-level composition keywords from
   `ArgMetadata`-equivalent metadata).
2. Re-run `muster test --generate-schema` to regenerate
   `schema.json`.
3. Validate the regenerated `schema.json` parses under JSON Schema
   2020-12 (the [SEP-2484 conformance suite](https://github.com/modelcontextprotocol/conformance)
   is the obvious target; see
   [08-protocol-evolution.md](08-protocol-evolution.md)).

If `schema.json` ends up needing manual edits to pass 2020-12
validation, that is a regression in the generator, not in
`schema.json`, and the fix belongs in the generator code path.

## 4. Required changes / migration notes

The work decomposes into one mandatory dependency bump, one mandatory
audit confirmation, and three opportunistic but recommended muster-side
improvements.

### 4.1 Mandatory: bump `mark3labs/mcp-go` to a SEP-2106 / SEP-2164-aware release

Both SEPs are wire-format changes inside mcp-go's domain:

- `Tool.inputSchema` and `Tool.outputSchema` need to be permissive
  containers (`map[string]any` or equivalent) rather than the
  `2025-11-25` subset.
- `CallToolResult.structuredContent` needs to type as `any` /
  `unknown`, not `map[string]any`.
- The resource-not-found server error code needs to be `-32602` and
  the response `data.uri` needs to be populated.

When mcp-go ships a release tagged as `2026-07-28`-aware, the muster
bump is a one-line `go.mod` change plus a `go mod tidy`. Until then,
muster's `tools/list` for tools defined in mcp-go-shaped structs is
**capped at the 2025-11-25 subset** and muster's `resources/read`
error responses are **non-conforming on the wire** for
`2026-07-28`. There is nothing muster's own Go can do to fix either
of those without forking the library.

### 4.2 Mandatory: keep the `-32002` audit clean

The §3.4 grep returned zero Go-source matches today. Land a
conformance scenario or a linter check that fails CI if a future PR
adds a literal `-32002` anywhere under `internal/`, `pkg/`, or
`cmd/`. The scenario can sit alongside the others in
[internal/testing/scenarios/](../../../internal/testing) — see the
process discussion in
[08-protocol-evolution.md](08-protocol-evolution.md) — and is
strictly cheaper than discovering the regression in production.

### 4.3 Recommended: add `outputSchema` to muster's own structured-result tools

Workflows whose result is a structured list (`workflow_list`,
`service_list`, `mcpserver_list`, the meta-tool `list_resources`,
etc.) should declare an `outputSchema` and start populating
`structuredContent` alongside their existing text content. The
SEP-2106-compliant pattern is:

- Define the `outputSchema` as a top-level `{ type: "array", items: { … } }`
  (no wrapper object) where the result is naturally a list.
- Populate `mcp.CallToolResult.StructuredContent` from
  [convertToMCPResult](../../../internal/aggregator/tool_factory.go)
  using the already-marshalled JSON bytes the function computes (it
  already does the marshal; today it discards the structured value
  into a text block).
- Keep emitting the marshalled JSON as a `TextContent` block for
  backwards compatibility with pre-SEP clients (SEP-2106 says servers
  using array or primitive `structuredContent` MUST also emit a
  serialized TextContent fallback).

This is a backwards-compatible muster change: old clients still see
the same text result they see today; SEP-2106-aware clients see the
structured value too.

### 4.4 Recommended: bound forwarded schema depth and reject network `$ref`s

The §1.1 security implications translate directly into muster:

- The aggregator MUST NOT auto-dereference `$ref` values when it
  forwards an upstream tool definition. Today muster forwards the
  schema verbatim and does no dereferencing, so the MUST is already
  satisfied; the work is to **assert** that in a test
  ([internal/aggregator/tool_factory_test.go](../../../internal/aggregator/tool_factory_test.go),
  which exists; the file currently has no test referencing
  `tool_factory_test` — see the `tool_factory_test` grep in §3 — and
  should grow one that forwards a schema with a network-pointing
  `$ref` and asserts muster did not fetch it).
- The aggregator SHOULD bound forwarded schema depth and total
  subschemas before exposing the result. A simple bound — refuse to
  forward a tool whose schema's JSON-serialized form exceeds, say,
  256 KiB or whose nesting depth exceeds, say, 64 — is enough to
  satisfy the SHOULD without writing a full validator. The bound
  belongs near
  [createMetaToolsFromProvider](../../../internal/aggregator/tool_factory.go)
  / `getAllCoreToolsAsMCPTools` so it applies uniformly to muster's
  own tools and to forwarded upstream tools.

### 4.5 Recommended: update CLI and operator docs to reference `-32602`

The CLI exit-code documentation
([docs/reference/cli/check.md](../../reference/cli/check.md),
[docs/reference/cli/get.md](../../reference/cli/get.md),
[docs/reference/cli/start.md](../../reference/cli/start.md),
[docs/reference/cli/README.md](../../reference/cli/README.md),
[docs/reference/README.md](../../reference/README.md)) all carry a
table row `2 | Resource not found`. That exit code is not affected by
SEP-2164. But the operator-facing
[docs/reference/api.md](../../reference/api.md) at line 698 still
shows `"text": "Error: Resource not found"` in an example response —
add a sentence to that doc explaining that the underlying JSON-RPC
error code is `-32602` (per SEP-2164) once the mcp-go bump in §4.1
lands, and that clients SHOULD continue to accept `-32002` for the
deprecation window.

### 4.6 Migration note for workflow authors

The `WorkflowInputSchema` deprecation in
[internal/api/workflow.go:166-181](../../../internal/api/workflow.go)
should be completed before SEP-2106 lands in muster, because
`SchemaProperty` (which `WorkflowInputSchema` references) only
carries `Type`/`Description`/`Default` and cannot express the new
JSON Schema 2020-12 vocabulary that workflows might legitimately want
to use (e.g. `enum` constraints on a region argument, `oneOf` to
accept either a name or an id). The migration is the one already
recommended in the GoDoc: replace `WorkflowInputSchema` with
`Args map[string]ArgDefinition`, then let `ArgMetadata.Schema`
(§3.1) carry whatever JSON Schema the author writes.

## 5. Open questions

The following items cannot be settled from the spec text alone and
should be tracked as separate issues once this document is in.

1. **mcp-go release alignment.** Is there an mcp-go release tagged
   against MCP `2026-07-28`? If not, what is the upstream owner's
   ETA, and is there a "Tier 1 SDK" expectation for it under
   [SDK tier docs](https://modelcontextprotocol.io/docs/sdk) that
   muster can rely on? This decides whether muster can adopt
   SEP-2106 / SEP-2164 transparently or has to pin a pre-release.
2. **Top-level composition keywords on muster's own tools.** Does
   any muster meta-tool or workflow tool today have a natural
   `oneOf` / `anyOf` shape on its top-level input that we are
   currently flattening into a single object? If yes, the
   `ArgMetadata` → `mcp.ToolInputSchema` plumbing in §3.1 needs to
   grow a way to declare top-level composition keywords.
3. **`outputSchema` per workflow type.** Should `outputSchema` be
   declared centrally for the meta-tools (`list_tools`,
   `list_resources`, …) or per workflow as a workflow YAML field?
   Per workflow is more flexible but requires plumbing through the
   workflow loader; central declaration is less work but only
   covers the meta-tools.
4. **Depth / size bounds.** What concrete numbers should muster pick
   for the schema-depth and total-size bounds suggested in §4.4?
   The SEP-2106 text is deliberately unspecific. A first cut of
   `maxDepth = 64`, `maxSize = 256 KiB` is reasonable but should be
   validated against the largest schema mcp-go has emitted in
   practice.
5. **`-32002` lint.** Should the `-32002` audit be enforced as a
   golangci-lint check (e.g. a custom analyzer or a `go-vet`-style
   pass), as a `goconst` config, or as a BDD scenario in
   `internal/testing/scenarios/`? The architecture rule prefers
   scenarios as the source of truth for behaviour, but a lint
   prevents the regression statically.
6. **Empty-`contents` enforcement at the forwarding boundary.** If a
   non-conforming upstream MCP server returns `{"contents": []}`
   for a missing resource, should muster's aggregator translate
   that into a `-32602` error on the way out (active enforcement)
   or forward the empty response unchanged (passive forwarding)?
   The SEP says servers MUST NOT return empty contents; it doesn't
   say aggregators MUST repair non-conforming upstreams. A
   decision belongs in the aggregator-policy section of
   [02-extensions-first-class.md](02-extensions-first-class.md)
   alongside the other "what muster does with non-conforming
   upstreams" questions.
7. **`schema.json` regeneration.** Once SEP-2106 lands in mcp-go,
   does `muster test --generate-schema` produce a `schema.json`
   that validates under JSON Schema 2020-12 without manual edits?
   If not, the generator needs work before the regenerated
   `schema.json` can be committed.

## 6. References

- Announcement, "Full JSON Schema 2020-12 for Tools" section:
  [https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
- [SEP-2106 (PR #2106) — Tools inputSchema & outputSchema Conform to JSON Schema 2020-12](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2106)
- [SEP-2164 (PR #2164) — Standardize Resource Not Found Error Code](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2164)
- [Draft tools spec (/specification/draft/server/tools)](https://modelcontextprotocol.io/specification/draft/server/tools)
- [Draft resources spec (/specification/draft/server/resources)](https://modelcontextprotocol.io/specification/draft/server/resources)
- [Draft changelog (records both items)](https://modelcontextprotocol.io/specification/draft/changelog)
- [JSON Schema 2020-12](https://json-schema.org/draft/2020-12)
- [SEP-2484 — Conformance tests required for final SEPs](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2484) (the conformance gate this SEP must clear)
- [SDK tier docs (/docs/sdk)](https://modelcontextprotocol.io/docs/sdk) (mcp-go tier expectations)
- [Conformance suite repo (modelcontextprotocol/conformance)](https://github.com/modelcontextprotocol/conformance)
- Muster source references used in §3:
  - [internal/aggregator/tool_factory.go](../../../internal/aggregator/tool_factory.go)
  - [internal/api/types.go](../../../internal/api/types.go)
  - [internal/api/workflow.go](../../../internal/api/workflow.go)
  - [internal/api/json_keys.go](../../../internal/api/json_keys.go)
  - [internal/api/errors.go](../../../internal/api/errors.go)
  - [internal/metatools/handlers.go](../../../internal/metatools/handlers.go)
  - [internal/aggregator/server.go](../../../internal/aggregator/server.go)
  - [cmd/get.go](../../../cmd/get.go)
  - [docs/reference/api.md](../../reference/api.md)
  - [docs/reference/cli/check.md](../../reference/cli/check.md),
    [docs/reference/cli/get.md](../../reference/cli/get.md),
    [docs/reference/cli/start.md](../../reference/cli/start.md),
    [docs/reference/cli/README.md](../../reference/cli/README.md),
    [docs/reference/README.md](../../reference/README.md)
- Cross-references inside this series:
  - [04-tasks-extension.md](04-tasks-extension.md) (the only other
    place `-32002` is mentioned in this docs set)
  - [06-deprecations-roots-sampling-logging.md](06-deprecations-roots-sampling-logging.md)
    (template alignment)
  - [08-protocol-evolution.md](08-protocol-evolution.md)
    (conformance scenarios and lint policy)
  - [02-extensions-first-class.md](02-extensions-first-class.md)
    (aggregator policy for non-conforming upstreams)
