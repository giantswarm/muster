package cli

import (
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/briandowns/spinner"

	"github.com/giantswarm/muster/v5/internal/agent"
)

// progressPrinter writes the progress a tool call reports to w (stderr), one
// line per notification, pausing the spinner around each line so the two do
// not interleave.
type progressPrinter struct {
	w       io.Writer
	spinner *spinner.Spinner

	mu   sync.Mutex
	done bool
}

func (p *progressPrinter) print(update agent.Progress) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.done {
		return
	}
	if p.spinner != nil {
		p.spinner.Stop()
		defer p.spinner.Start()
	}
	_, _ = fmt.Fprintln(p.w, formatProgress(update))
}

// stop stops the spinner for good: progress that arrives after the call
// answered is not printed.
func (p *progressPrinter) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.done = true
	if p.spinner != nil {
		p.spinner.Stop()
	}
}

// formatProgress renders one progress update: its message, and the progress
// counted against the total when the server sent one.
func formatProgress(update agent.Progress) string {
	count := strconv.FormatFloat(update.Progress, 'f', -1, 64)
	if update.Total != nil {
		count += "/" + strconv.FormatFloat(*update.Total, 'f', -1, 64)
	}
	if update.Message == "" {
		return "… " + count
	}
	return fmt.Sprintf("… %s (%s)", update.Message, count)
}
