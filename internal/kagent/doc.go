// Package kagent is muster's client of the kagent controller's
// SessionService. muster reads a Session as the person who presents it, with
// that person's own bearer, so the controller answers only that person's
// Sessions.
//
// The stubs under gen/ are generated from the kagent protos copied under
// hack/kagent-proto/ (make generate-kagent).
package kagent
