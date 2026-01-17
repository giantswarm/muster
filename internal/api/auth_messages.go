package api

// Auth tool response message markers.
// These constants define the standard message prefixes/markers used in auth tool responses.
// They enable consistent detection of connection status across CLI and aggregator components.
//
// IMPORTANT: When modifying these values, ensure both the message generation (auth_tools.go)
// and message detection (auth_helpers.go) code remain synchronized.
const (
	// AuthMsgAlreadyConnected indicates the user already has an active session connection
	// to the specific server they're trying to authenticate to.
	AuthMsgAlreadyConnected = "Already Connected"

	// AuthMsgSuccessfullyConnected indicates a successful connection was established,
	// either through SSO token reuse or direct authentication.
	AuthMsgSuccessfullyConnected = "Successfully connected"

	// AuthMsgAlreadyAuthenticated is an alternative marker for existing authentication.
	AuthMsgAlreadyAuthenticated = "already authenticated"
)
