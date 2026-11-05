//go:build !hublinktest

package config

// hubSignInTestOriginAllowed is false in every build but the local end-to-end
// harness's: hub sign-in never runs against a hub at a test origin in a
// release (hub-link-protocol.md section 2.3).
const hubSignInTestOriginAllowed = false
