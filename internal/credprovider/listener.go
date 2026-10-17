package credprovider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	"github.com/giantswarm/muster/v5/pkg/proto/credproviderpb"
)

// DefaultListenAddress is the FetchSecret listener's default address.
const DefaultListenAddress = ":50051"

// Server owns the FetchSecret gRPC listener.
type Server struct {
	grpc *grpc.Server
	lis  net.Listener
	done chan struct{}
}

// TLSFiles names the PEM files the listener serves with.
type TLSFiles struct {
	// ServerCredBundle holds the serving key and certificate chain.
	ServerCredBundle string
	// ClientCAFile holds the CAs a caller's certificate must chain to.
	ClientCAFile string
}

// Listen binds addr and serves the provider over mutual TLS: the caller's
// certificate must chain to files.ClientCAFile and carry the provider's
// gateway identity as a URI SAN. Both files are re-read when they change, so
// rotated pod certificates and trust bundles are picked up.
func Listen(ctx context.Context, addr string, files TLSFiles, p *Provider) (*Server, error) {
	if files.ServerCredBundle == "" || files.ClientCAFile == "" {
		return nil, fmt.Errorf("credential provider: serverCredBundle and clientCAFile are required")
	}
	cfg, err := serverTLSConfig(files, p.GatewayIdentity())
	if err != nil {
		return nil, err
	}
	if addr == "" {
		addr = DefaultListenAddress
	}
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("credential provider: listen on %s: %w", addr, err)
	}
	return Serve(lis, credentials.NewTLS(cfg), p), nil
}

// Serve serves the provider on lis with creds until Stop.
func Serve(lis net.Listener, creds credentials.TransportCredentials, p *Provider) *Server {
	s := &Server{grpc: grpc.NewServer(grpc.Creds(creds)), lis: lis, done: make(chan struct{})}
	credproviderpb.RegisterCredentialProviderServer(s.grpc, p)
	go func() {
		defer close(s.done)
		_ = s.grpc.Serve(lis)
	}()
	return s
}

// Addr is the bound listen address.
func (s *Server) Addr() string { return s.lis.Addr().String() }

// Stop drains in-flight calls until ctx ends, then stops hard.
func (s *Server) Stop(ctx context.Context) {
	stopped := make(chan struct{})
	go func() {
		s.grpc.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-ctx.Done():
		s.grpc.Stop()
	}
	<-s.done
}

// serverTLSConfig requires TLS 1.3 and a client certificate chaining to the
// CA file and naming identity.
func serverTLSConfig(files TLSFiles, identity string) (*tls.Config, error) {
	bundle := &reloadingFile{path: files.ServerCredBundle}
	cas := &reloadingFile{path: files.ClientCAFile}
	if _, err := bundle.certificate(); err != nil {
		return nil, err
	}
	if _, err := cas.pool(); err != nil {
		return nil, err
	}
	base := &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return bundle.certificate()
		},
	}
	base.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		pool, err := cas.pool()
		if err != nil {
			return nil, err
		}
		c := base.Clone()
		c.GetConfigForClient = nil
		c.ClientAuth = tls.RequireAndVerifyClientCert
		c.ClientCAs = pool
		c.VerifyConnection = requireURISAN(identity)
		return c, nil
	}
	return base, nil
}

// requireURISAN accepts a peer whose leaf certificate carries identity.
func requireURISAN(identity string) func(tls.ConnectionState) error {
	return func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("a client certificate is required")
		}
		for _, u := range state.PeerCertificates[0].URIs {
			if u.String() == identity {
				return nil
			}
		}
		return fmt.Errorf("client certificate does not name the egress gateway %q", identity)
	}
}

// callerIdentity returns the first URI SAN of the verified peer certificate,
// "" without one.
func callerIdentity(ctx context.Context) string {
	pr, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	info, ok := pr.AuthInfo.(credentials.TLSInfo)
	if !ok || len(info.State.VerifiedChains) == 0 || len(info.State.PeerCertificates) == 0 {
		return ""
	}
	for _, u := range info.State.PeerCertificates[0].URIs {
		if u.Scheme == "spiffe" {
			return u.String()
		}
	}
	return ""
}

// reloadingFile parses a PEM file again whenever its modification time or
// size changes.
type reloadingFile struct {
	path string

	mu      sync.Mutex
	modTime time.Time
	size    int64
	cert    *tls.Certificate
	certs   *x509.CertPool
}

func (f *reloadingFile) read() ([]byte, bool, error) {
	info, err := os.Stat(f.path)
	if err != nil {
		return nil, false, fmt.Errorf("credential provider: %w", err)
	}
	if info.ModTime().Equal(f.modTime) && info.Size() == f.size && (f.cert != nil || f.certs != nil) {
		return nil, false, nil
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, false, fmt.Errorf("credential provider: %w", err)
	}
	f.modTime, f.size = info.ModTime(), info.Size()
	return data, true, nil
}

func (f *reloadingFile) certificate() (*tls.Certificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, changed, err := f.read()
	if err != nil {
		return nil, err
	}
	if changed {
		cert, err := tls.X509KeyPair(data, data)
		if err != nil {
			return nil, fmt.Errorf("credential provider: %s: %w", f.path, err)
		}
		f.cert = &cert
	}
	return f.cert, nil
}

func (f *reloadingFile) pool() (*x509.CertPool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, changed, err := f.read()
	if err != nil {
		return nil, err
	}
	if changed {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("credential provider: no certificates in %s", f.path)
		}
		f.certs = pool
	}
	return f.certs, nil
}
