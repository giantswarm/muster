package api

// CallerAuthenticationError means the caller's token cannot authenticate at
// a service muster needs to authorize the call.
type CallerAuthenticationError struct {
	Message string
}

func (e *CallerAuthenticationError) Error() string {
	return e.Message
}
