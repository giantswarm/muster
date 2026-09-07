// Package toolset implements declared toolsets: the selector list an agent
// sends in the X-Muster-Toolset header (or passes to filter_tools) that bounds
// which of the gateway's tools its meta-tools can see and call.
//
// A toolset is evaluated per request, statelessly, as the third filter on the
// per-session catalogue — after server-authentication visibility and workflow
// availability. It never widens access: the catalogue it filters is already
// the caller's own.
//
// Inline grammar (header, filter_tools argument): preset:<name>, server:<name>,
// workflow:<name>, tool:<name> with exact names, at most MaxInlineSelectors.
// toolset:<name> is reserved; label: exists inside presets only. Patterns,
// excludes, the read-only predicate and composition live in presets, which are
// muster configuration (toolsetPresets) with read-only, none and full built in.
//
// The package has one scope-source seam, SourceFromRequest: the header is the
// first source of a request's toolset, an actor claim from a future agent
// identity the second. Parsing, resolution and the refusal do not change with
// the source.
package toolset
