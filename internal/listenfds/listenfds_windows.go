//go:build windows

package listenfds

import (
	"errors"
	"net"
)

const supported = false

func fileListener(int, string) (net.Listener, error) {
	return nil, errors.New("inherited listeners are not supported on windows")
}
