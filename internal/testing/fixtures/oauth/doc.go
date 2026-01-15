// Package oauth contains test fixtures for OAuth authentication testing.
//
// The fixtures in this directory provide sample data for testing OAuth flows:
//
//   - valid_token.json: A sample valid OAuth token response
//   - expired_token.json: A sample expired OAuth token for testing expiry handling
//   - metadata.json: Sample OAuth 2.1 authorization server metadata
//   - www_authenticate.txt: Example WWW-Authenticate headers for testing auth challenges
//
// These fixtures can be loaded in tests using:
//
//	import "embed"
//
//	//go:embed valid_token.json
//	var ValidTokenFixture []byte
package oauth
