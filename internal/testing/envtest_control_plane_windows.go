//go:build windows

package testing

import (
	"errors"
	"net/url"
	"strings"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// kubernetesModeSupported: controller-runtime's envtest package does not build
// on Windows, so the muster binary carries no control plane here and every
// mode: kubernetes scenario is reported as skipped.
const kubernetesModeSupported = false

// envtestControlPlane is the Windows stand-in for the control plane the other
// platforms start: it never starts, and says why.
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
