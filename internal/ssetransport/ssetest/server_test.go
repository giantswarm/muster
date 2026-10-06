package ssetest_test

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/ssetransport"
	"github.com/giantswarm/muster/v5/internal/ssetransport/ssetest"
)

// TestHeldStreamClosesWithoutAReaderInRead proves the holding server lets a
// wrapped client close the held stream's body when no Read is in flight: the
// order in which ssetransport closes the body at once, and no Read reaches the
// connection again.
func TestHeldStreamClosesWithoutAReaderInRead(t *testing.T) {
	url := ssetest.NewHoldingServer(t)
	client := ssetransport.Client(nil)

	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "event: message\n" {
		t.Fatalf("the answer's first line = %q, %v", line, err)
	}

	closed := make(chan error, 1)
	go func() { closed <- resp.Body.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("closing the body: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing the held stream's body with no Read in flight blocks")
	}
}
