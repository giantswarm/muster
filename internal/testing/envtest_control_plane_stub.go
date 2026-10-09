//go:build windows || !envtest

package testing

import (
	"errors"
	"net/url"
	"strings"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// kubernetesModeSupported: the muster binary carries no control plane unless it
// is built with the envtest tag (make test-envtest does), and controller-runtime's
// envtest package does not build on Windows at all. Every mode: kubernetes
// scenario is then reported as skipped. The tag keeps envtest, whose package
// init creates a cache directory and panics without a writable /tmp, out of the
// release binary and the scratch image.
const kubernetesModeSupported = false

// envtestControlPlane is the stand-in for the control plane an envtest build
// starts: it never starts, and says why.
type envtestControlPlane struct {
	config *rest.Config
	client client.Client
}

func (cp *envtestControlPlane) start(TestLogger, bool) error {
	return errors.New(kubernetesModeUnavailableReason())
}

func (cp *envtestControlPlane) apiServerAddr() string {
	if cp.config == nil {
		return ""
	}
	if u, err := url.Parse(cp.config.Host); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(cp.config.Host, "https://"), "http://"), "/")
}

func (cp *envtestControlPlane) stop() error { return nil }
