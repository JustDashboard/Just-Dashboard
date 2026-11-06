//go:build hublinktest

package config

// hubSignInTestOriginAllowed lets the local end-to-end harness, which builds
// with -tags hublinktest, turn hub sign-in on against its hub at the test
// origin. Release builds never carry the tag.
const hubSignInTestOriginAllowed = true
