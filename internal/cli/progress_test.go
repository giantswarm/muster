package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/v5/internal/agent"
)

func TestFormatProgress(t *testing.T) {
	total := float64(4)
	assert.Equal(t, "… waiting for checks (2/4)", formatProgress(agent.Progress{Progress: 2, Total: &total, Message: "waiting for checks"}))
	assert.Equal(t, "… waiting for checks (0.5)", formatProgress(agent.Progress{Progress: 0.5, Message: "waiting for checks"}))
	assert.Equal(t, "… 3", formatProgress(agent.Progress{Progress: 3}))
}

// The printer writes one line per update and none once the call answered.
func TestProgressPrinterStopsAtTheAnswer(t *testing.T) {
	var out bytes.Buffer
	printer := &progressPrinter{w: &out}

	printer.print(agent.Progress{Progress: 1, Message: "queued"})
	printer.stop()
	printer.print(agent.Progress{Progress: 2, Message: "late"})

	assert.Equal(t, "… queued (1)\n", out.String())
}
