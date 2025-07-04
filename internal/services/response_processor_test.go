package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProcessToolOutputs(t *testing.T) {
	tests := []struct {
		name     string
		response map[string]interface{}
		outputs  map[string]string
		expected map[string]interface{}
	}{
		{
			name: "simple field extraction",
			response: map[string]interface{}{
				"sessionID": "mock-session-12345",
				"status":    "started",
			},
			outputs: map[string]string{
				"sessionID": "sessionID",
				"status":    "status",
			},
			expected: map[string]interface{}{
				"sessionID": "mock-session-12345",
				"status":    "started",
			},
		},
		{
			name: "nested field extraction",
			response: map[string]interface{}{
				"result": map[string]interface{}{
					"connectionId": "conn-123",
					"status":       "connected",
				},
			},
			outputs: map[string]string{
				"connectionId": "result.connectionId",
				"status":       "result.status",
			},
			expected: map[string]interface{}{
				"connectionId": "conn-123",
				"status":       "connected",
			},
		},
		{
			name: "missing field",
			response: map[string]interface{}{
				"status": "started",
			},
			outputs: map[string]string{
				"sessionID": "sessionID",
				"status":    "status",
			},
			expected: map[string]interface{}{
				"status": "started",
				// sessionID should be missing since it's not in response
			},
		},
		{
			name:     "empty outputs",
			response: map[string]interface{}{"test": "value"},
			outputs:  map[string]string{},
			expected: nil, // Changed from map[string]interface{}{} to nil since we return nil for empty results
		},
		{
			name:     "nil outputs",
			response: map[string]interface{}{"test": "value"},
			outputs:  nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProcessToolOutputs(tt.response, tt.outputs)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractFromResponse(t *testing.T) {
	tests := []struct {
		name     string
		response map[string]interface{}
		path     string
		expected interface{}
	}{
		{
			name: "direct field",
			response: map[string]interface{}{
				"sessionID": "value123",
			},
			path:     "sessionID",
			expected: "value123",
		},
		{
			name: "nested field",
			response: map[string]interface{}{
				"result": map[string]interface{}{
					"id": "nested-value",
				},
			},
			path:     "result.id",
			expected: "nested-value",
		},
		{
			name: "missing field",
			response: map[string]interface{}{
				"other": "value",
			},
			path:     "missing",
			expected: nil,
		},
		{
			name:     "empty path",
			response: map[string]interface{}{"test": "value"},
			path:     "",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractFromResponse(tt.response, tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}
