package workflow

import (
	"fmt"
	"math"

	"github.com/giantswarm/muster/v5/internal/api"
)

// convertToolOptions reads a step's or sub-step's paginate and jq fields from
// structured create/validate arguments.
func convertToolOptions(m map[string]interface{}) (*api.WorkflowPaginate, string, error) {
	var paginate *api.WorkflowPaginate
	if raw, ok := m["paginate"]; ok {
		obj, ok := raw.(map[string]interface{})
		if !ok {
			return nil, "", fmt.Errorf("invalid paginate: must be an object")
		}
		p, err := convertPaginate(obj)
		if err != nil {
			return nil, "", fmt.Errorf("invalid paginate: %w", err)
		}
		paginate = p
	}
	var program string
	if raw, ok := m["jq"]; ok {
		s, ok := raw.(string)
		if !ok {
			return nil, "", fmt.Errorf("jq must be a string holding a jq program")
		}
		program = s
	}
	return paginate, program, nil
}

// convertPaginate converts a paginate object to api.WorkflowPaginate.
func convertPaginate(m map[string]interface{}) (*api.WorkflowPaginate, error) {
	p := &api.WorkflowPaginate{}
	for key, dst := range map[string]*string{
		"itemsField": &p.ItemsField,
		"tokenField": &p.TokenField,
		"tokenArg":   &p.TokenArg,
	} {
		if raw, ok := m[key]; ok {
			s, ok := raw.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("%s must be a non-empty string", key)
			}
			*dst = s
		}
	}
	maxPages, err := boundedInt(m, "maxPages", api.MaxPaginateMaxPages)
	if err != nil {
		return nil, err
	}
	p.MaxPages = maxPages
	return p, nil
}

// boundedInt reads an optional integer field that, when present, must be
// between 1 and max. It returns 0 when the field is absent: an explicit 0 is
// an error rather than "not set".
func boundedInt(m map[string]interface{}, key string, max int) (int, error) {
	raw, ok := m[key]
	if !ok {
		return 0, nil
	}
	var n float64
	switch v := raw.(type) {
	case float64:
		n = v
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	default:
		return 0, fmt.Errorf("%s must be an integer between 1 and %d", key, max)
	}
	if n != math.Trunc(n) || n < 1 || n > float64(max) {
		return 0, fmt.Errorf("%s must be an integer between 1 and %d, got %v", key, max, raw)
	}
	return int(n), nil
}
