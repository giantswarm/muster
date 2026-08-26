package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/giantswarm/muster/internal/api"
)

// newSigV4Client returns a StreamableHTTP client that signs every request with
// AWS Signature Version 4.
//
// The credential is muster's own machine identity, not the caller's, so this
// client is the shared global one for the server. It is deliberately not routed
// through the session-scoped machinery: DynamicAuthClient and the connection
// pool exist to give each user their own credential, which is the opposite of
// what a machine identity is for.
//
// The credential provider is built on the first connect rather than here,
// because resolving it can call STS and the caller's connect context is the
// right one to bound that with.
//
// Unexported on purpose: NewMCPClientFromType validates the auth config before
// calling this, so cfg arrives with a region already checked.
func newSigV4Client(serverURL string, headers map[string]string, signing api.MCPServerSigV4, meta map[string]string) (*StreamableHTTPClient, error) {
	service := signing.Service
	if service == "" {
		var err error
		service, err = sigV4ServiceFromURL(serverURL)
		if err != nil {
			return nil, err
		}
	}

	return &StreamableHTTPClient{
		url:     serverURL,
		headers: headers,
		httpClientFunc: func(ctx context.Context) (*http.Client, error) {
			creds, err := sigV4Credentials(ctx, signing.Region, signing.RoleARN)
			if err != nil {
				return nil, err
			}
			// metaTransport wraps the signer, not the other way round: the body
			// has to be rewritten before it is signed, because the signature
			// covers a hash of the payload.
			//
			// No client timeout: this transport also carries the long-lived GET
			// listener WithContinuousListening opens, which a timeout would cut.
			return &http.Client{
				Transport: newMetaTransport(&sigV4Transport{
					base:    http.DefaultTransport,
					signer:  v4.NewSigner(),
					creds:   creds,
					region:  signing.Region,
					service: service,
				}, meta),
			}, nil
		},
	}, nil
}

// sigV4ServiceFromURL derives the signing service name from the first hostname
// label of a URL, which is how AWS's own clients derive it:
// aws-mcp.eu-central-1.api.aws signs as service "aws-mcp".
func sigV4ServiceFromURL(serverURL string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("cannot derive sigv4 service from url %q: %w", serverURL, err)
	}
	label, _, _ := strings.Cut(parsed.Hostname(), ".")
	if label == "" {
		return "", fmt.Errorf("cannot derive sigv4 service from url %q: no hostname", serverURL)
	}
	return label, nil
}
