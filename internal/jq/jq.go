package jq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/itchyny/gojq"
)

// variables are the names a program may read besides its input, in the order
// Run passes their values.
var variables = []string{"$input", "$vars"}

// Program is a compiled jq program.
type Program struct {
	code *gojq.Code
}

// Compile parses and compiles a jq program.
func Compile(source string) (*Program, error) {
	query, err := gojq.Parse(source)
	if err != nil {
		return nil, err
	}
	code, err := gojq.Compile(query, gojq.WithVariables(variables))
	if err != nil {
		return nil, err
	}
	return &Program{code: code}, nil
}

// Check reports why a jq program does not compile, nil when it does.
func Check(source string) error {
	_, err := Compile(source)
	return err
}

// Run runs the program on input with the workflow input and loop variables
// bound to $input and $vars. It returns the one value the program yields,
// nil when it yields none, and an error when it yields more than one or
// fails.
func (p *Program) Run(ctx context.Context, input interface{}, workflowInput, vars map[string]interface{}) (interface{}, error) {
	in, err := normalize(workflowInput)
	if err != nil {
		return nil, fmt.Errorf("$input: %w", err)
	}
	vs, err := normalize(vars)
	if err != nil {
		return nil, fmt.Errorf("$vars: %w", err)
	}
	iter := p.code.RunWithContext(ctx, input, in, vs)
	var out []interface{}
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			// `halt` stops the program without an error.
			var halt *gojq.HaltError
			if errors.As(err, &halt) && halt.Value() == nil {
				break
			}
			return nil, err
		}
		out = append(out, v)
	}
	switch len(out) {
	case 0:
		return nil, nil
	case 1:
		return out[0], nil
	default:
		return nil, fmt.Errorf("the program yielded %d values where one is stored; wrap it in [ ] to collect them into one list", len(out))
	}
}

// normalize turns a map built from YAML, JSON or Go literals into the
// JSON-shaped values gojq accepts (it rejects, among others, int64 and typed
// slices) by a round trip through JSON. The workflow input and loop variables
// are small, so the copy is cheap; a step result is already JSON-shaped.
func normalize(m map[string]interface{}) (interface{}, error) {
	if m == nil {
		return map[string]interface{}{}, nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// Cache holds compiled programs by their source, so a step inside a loop is
// compiled once, not once per iteration. It is safe for concurrent use.
type Cache struct {
	programs sync.Map // source -> *Program
}

// Get returns the compiled program for source, compiling it on first use.
func (c *Cache) Get(source string) (*Program, error) {
	if p, ok := c.programs.Load(source); ok {
		return p.(*Program), nil
	}
	p, err := Compile(source)
	if err != nil {
		return nil, err
	}
	actual, _ := c.programs.LoadOrStore(source, p)
	return actual.(*Program), nil
}
