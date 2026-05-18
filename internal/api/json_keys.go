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
