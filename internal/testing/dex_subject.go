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
// signInURL: the connector is the one the request names. A request naming
// none would show Dex's chooser, which a test cannot answer.
func dexSignInSubject(userID string, signInURL *url.URL) (string, error) {
	connector := signInURL.Query().Get("connector_id")
	if connector == "" {
		return "", fmt.Errorf("the sign-in check names no Dex connector, so no subject for user %q can be derived: set dex_connector_id on muster's identity provider", userID)
	}
	return dexSubject(userID, connector), nil
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
	return dexSignInSubject(p.dexUserID, parsed)
}

// identitySignInPersonFromArgs reads who is in the browser at muster's
// sign-in check from test_simulate_oauth_callback's arguments:
// "signed_in_as", their subject, or "signed_in_with_user", their Dex user ID,
// whose subject is the one Dex issues for the connector the sign-in names.
// Neither means the person the link was issued to.
func identitySignInPersonFromArgs(args map[string]interface{}) (identitySignInPerson, error) {
	var p identitySignInPerson
	p.subject, _ = args["signed_in_as"].(string)
	p.dexUserID, _ = args["signed_in_with_user"].(string)
	if p.subject != "" && p.dexUserID != "" {
		return identitySignInPerson{}, fmt.Errorf("signed_in_as and signed_in_with_user name the person in the browser twice: set one")
	}
	return p, nil
}
