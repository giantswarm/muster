package server

import (
	"encoding/base64"

	"google.golang.org/protobuf/encoding/protowire"
)

// Field numbers of Dex's IDTokenSubject message.
const (
	dexSubjectUserIDField    protowire.Number = 1
	dexSubjectConnectorField protowire.Number = 2
)

// dexSubjectConnector returns the connector a Dex sub claim was issued
// through. Dex issues the sub as its IDTokenSubject message (user_id,
// conn_id), protobuf-encoded and base64url without padding, so one person
// signing in through two connectors gets two subjects.
func dexSubjectConnector(subject string) (string, bool) {
	data, err := base64.RawURLEncoding.DecodeString(subject)
	if err != nil || len(data) == 0 {
		return "", false
	}
	var connector string
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return "", false
		}
		data = data[n:]
		if typ != protowire.BytesType || (num != dexSubjectUserIDField && num != dexSubjectConnectorField) {
			return "", false
		}
		value, n := protowire.ConsumeBytes(data)
		if n < 0 {
			return "", false
		}
		data = data[n:]
		if num == dexSubjectConnectorField {
			connector = string(value)
		}
	}
	return connector, connector != ""
}
