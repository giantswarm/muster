//go:build !windows

package testing

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// kubernetesModeSupported: the envtest control plane runs here.
const kubernetesModeSupported = true

// envtestControlPlane is the one API server a muster test run shares between
// its Kubernetes-mode instances, started on the first of them. Each instance
// has its own namespace and its own proxy in front of it; the control plane
// itself is never touched by a scenario.
//
// It lives in a file of its own with a build constraint because
// controller-runtime's envtest package does not build on Windows; the muster
// binary does, and there Kubernetes-mode scenarios are reported as skipped.
type envtestControlPlane struct {
	once     sync.Once
	env      *envtest.Environment
	config   *rest.Config
	client   client.Client
	startErr error
}

// start brings the control plane up once; later calls report the first
// result.
func (cp *envtestControlPlane) start(logger TestLogger, debug bool) error {
	cp.once.Do(func() {
		if reason := kubernetesModeUnavailableReason(); reason != "" {
			cp.startErr = errors.New(reason)
			return
		}
		crdDir, err := findCRDDirectory()
		if err != nil {
			cp.startErr = err
			return
		}
		env := &envtest.Environment{
			CRDDirectoryPaths:     []string{crdDir},
			ErrorIfCRDPathMissing: true,
		}
		cfg, err := env.Start()
		if err != nil {
			cp.startErr = fmt.Errorf("failed to start the envtest control plane: %w", err)
			return
		}
		scheme := runtime.NewScheme()
		utilruntime.Must(clientgoscheme.AddToScheme(scheme))
		utilruntime.Must(musterv1alpha1.AddToScheme(scheme))
		c, err := client.New(cfg, client.Options{Scheme: scheme})
		if err != nil {
			_ = env.Stop()
			cp.startErr = fmt.Errorf("failed to create the envtest client: %w", err)
			return
		}
		cp.env, cp.config, cp.client = env, cfg, c
		if debug {
			logger.Debug("☸️  Started envtest control plane at %s with CRDs from %s\n", cfg.Host, crdDir)
		}
	})
	return cp.startErr
}

// apiServerAddr is the host:port the proxies dial (envtest reports the API
// server as a URL with a trailing slash).
func (cp *envtestControlPlane) apiServerAddr() string {
	if u, err := url.Parse(cp.config.Host); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(cp.config.Host, "https://"), "http://"), "/")
}

// stop tears the control plane down; a no-op when it never started.
func (cp *envtestControlPlane) stop() error {
	if cp.env == nil {
		return nil
	}
	err := cp.env.Stop()
	cp.env = nil
	return err
}
