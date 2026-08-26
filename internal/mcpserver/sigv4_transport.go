package mcpserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// ErrAWSCredentialsUnavailable reports that the signing transport could not
// resolve AWS credentials. It wraps the SDK's own error, and the service layer
// treats it as a transient connectivity failure so the reconnect backoff picks
// the server up once the credential becomes available. Every cause is external
// and fixable while muster keeps running: a projected web-identity token that
// has not appeared yet, a role that is not deployed, a trust policy being
// corrected, an STS outage.
var ErrAWSCredentialsUnavailable = errors.New("sigv4: failed to retrieve AWS credentials")

// contentSHA256Header carries the hex payload hash that the signature covers.
//
// v4.Signer.SignHTTP does not add it; in the SDK it is a separate
// ContentSHA256Header middleware, so a hand-rolled signer has to set it. S3 and
// the services that share its request model reject a signed request without it,
// and auth.sigv4.service is user-settable, so muster cannot assume the endpoint
// is one of the services that ignore it. Set before signing, never after, so
// the header travels inside SignedHeaders.
const contentSHA256Header = "X-Amz-Content-Sha256"

// sigV4Transport signs every outbound request with AWS Signature Version 4.
//
// It is an http.RoundTripper rather than an mcp-go transport option because
// mcp-go deliberately vendors no provider SDK: WithHTTPBasicClient exists so
// provider-specific signing stays outside the library.
//
// It signs the body it is given and never rewrites it. Any rewrite belongs to
// an outer transport, because SigV4 covers a hash of the payload: a signature
// computed over one body and sent with another is rejected by the endpoint.
// See newSigV4Client for the chain that puts metaTransport in front of this.
type sigV4Transport struct {
	// base carries the signed request. Never nil in constructed values.
	base http.RoundTripper

	// signer holds no state beyond its options, so one instance serves every
	// request on this transport.
	signer *v4.Signer

	// creds is the per-MCPServer credential provider. It is wrapped in an
	// aws.CredentialsCache, so Retrieve is cheap after the first call.
	creds aws.CredentialsProvider

	// region is the signing region, which the endpoint checks against the
	// credential scope of the signature.
	region string

	service string
}

// RoundTrip signs a clone of req and forwards it to the base transport.
//
// req's headers and fields are not modified, and its body is read through
// req.GetBody so the caller's copy stays readable. The body is closed before
// returning, which the http.RoundTripper contract requires of every path,
// including the error paths.
func (t *sigV4Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}

	body, err := readRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("sigv4: failed to read request body: %w", err)
	}

	signed := req.Clone(req.Context())

	// Connection is a hop-by-hop header: net/http manages it on the wire, so a
	// signature that covers it never matches what the endpoint verifies. AWS's
	// own clients exclude it too; the SigV4 signer's ignore list does not.
	signed.Header.Del("Connection")

	if body != nil {
		signed.Body = io.NopCloser(bytes.NewReader(body))
		// Content-Length is signed from req.ContentLength, not from the header
		// map, so setting the header instead would leave it out of the
		// signature and break every request with a body.
		signed.ContentLength = int64(len(body))
		signed.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}

	creds, err := t.creds.Retrieve(req.Context())
	if err != nil {
		// Wrapped in a sentinel so the service layer can classify it as
		// retryable without matching on the message. The causes are all
		// externally fixable — a projected token not there yet, a role not
		// deployed, an STS blip — so the server must not settle into a
		// permanent failure that only a pod restart clears.
		return nil, fmt.Errorf("%w: %w", ErrAWSCredentialsUnavailable, err)
	}

	payloadHash := sha256.Sum256(body)
	payloadHashHex := hex.EncodeToString(payloadHash[:])
	signed.Header.Set(contentSHA256Header, payloadHashHex)

	if err := t.signer.SignHTTP(
		req.Context(), creds, signed, payloadHashHex,
		t.service, t.region, time.Now().UTC(),
	); err != nil {
		return nil, fmt.Errorf("sigv4: failed to sign request: %w", err)
	}

	return t.base.RoundTrip(signed)
}
