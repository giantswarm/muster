package aggregator

import (
	"testing"

	"github.com/giantswarm/muster/internal/aggregator"

	"github.com/stretchr/testify/assert"
)

func TestNewAggregatorService(t *testing.T) {
	config := aggregator.AggregatorConfig{
		Host: "localhost",
		Port: 8080,
	}

	service := NewAggregatorService(config, nil, nil)

	assert.NotNil(t, service)
	assert.Equal(t, "mcp-aggregator", service.GetName())
	assert.Equal(t, 0, len(service.GetDependencies()), "Should have no dependencies by default")
}
