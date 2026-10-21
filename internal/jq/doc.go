// Package jq compiles and runs the jq programs a workflow step reshapes its
// result with (the step's `jq` field), on gojq, a pure-Go jq.
//
// A program sees the step's result as its input and the workflow input and
// loop variables as $input and $vars. It must yield one value: a program
// that yields a stream is wrapped in [ ] to collect it, so the stored result
// never depends on how many rows the data happened to hold.
package jq
