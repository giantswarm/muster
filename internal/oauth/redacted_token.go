package oauth

// RedactedToken wraps a sensitive token string to prevent accidental logging.
//
// This type implements fmt.Stringer to return "[REDACTED]" instead of the actual
// token value, preventing accidental credential leakage in log messages, error
// strings, or debug output.
//
// Usage:
//
//	token := oauth.NewRedactedToken("secret-token-value")
//	fmt.Println(token)        // prints: [REDACTED]
//	actualValue := token.Value() // returns: "secret-token-value"
type RedactedToken struct {
	value string
}

// NewRedactedToken creates a new RedactedToken wrapping the given value.
func NewRedactedToken(value string) RedactedToken {
	return RedactedToken{value: value}
}

// Value returns the actual token value.
// Use this method only when the token needs to be sent in an HTTP header or
// similar authenticated request. Never log the result of this method.
func (t RedactedToken) Value() string {
	return t.value
}

// String implements fmt.Stringer, returning "[REDACTED]" to prevent
// accidental logging of the token value.
func (t RedactedToken) String() string {
	return "[REDACTED]"
}

// GoString implements fmt.GoStringer for %#v formatting, also returning
// "[REDACTED]" to prevent accidental logging.
func (t RedactedToken) GoString() string {
	return "oauth.RedactedToken{[REDACTED]}"
}

// IsEmpty returns true if the token value is empty.
func (t RedactedToken) IsEmpty() bool {
	return t.value == ""
}

// MarshalText implements encoding.TextMarshaler, returning "[REDACTED]"
// to prevent accidental serialization of the token value.
func (t RedactedToken) MarshalText() ([]byte, error) {
	return []byte("[REDACTED]"), nil
}

// MarshalJSON implements json.Marshaler, returning "[REDACTED]"
// to prevent accidental JSON serialization of the token value.
func (t RedactedToken) MarshalJSON() ([]byte, error) {
	return []byte(`"[REDACTED]"`), nil
}

