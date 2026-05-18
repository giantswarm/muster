package commands

// Stringly-typed boolean literals produced by the CLI's positional argument
// parsing. The CLI accepts case-insensitive "true"/"false"; commands compare
// lowercased input against these.
const (
	boolStringTrue  = "true"
	boolStringFalse = "false"
)
