package testing

import (
	"encoding/base64"
	"fmt"
	"net/url"

	"google.golang.org/protobuf/encoding/protowire"
)

// dexSubject is the sub claim Dex issues to userID signing in through
// connectorID: its IDTokenSubject message (user_id 1, conn_id 2),
// protobuf-encoded and base64url without padding.
func dexSubject(userID, connectorID string) string {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, userID)
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendString(b, connectorID)
	return base64.RawURLEncoding.EncodeToString(b)
}

// dexSignInSubject is the sub Dex issues to userID for the sign-in request at
// signInURL: the connector is the one the request names.
func dexSignInSubject(userID string, signInURL *url.URL) string {
	return dexSubject(userID, signInURL.Query().Get("connector_id"))
}

// identitySignInPerson is the person in the browser at muster's sign-in
// check: a fixed subject, or a Dex user ID whose subject depends on the
// connector the sign-in names.
type identitySignInPerson struct {
	subject   string
	dexUserID string
}

// subjectAt is the subject the person signs in with at the sign-in request
// signInURL; empty when the test names no one.
func (p identitySignInPerson) subjectAt(signInURL string) (string, error) {
	if p.dexUserID == "" {
		return p.subject, nil
	}
	parsed, err := url.Parse(signInURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse sign-in check URL: %w", err)
	}
	return dexSignInSubject(p.dexUserID, parsed), nil
}
