package deploy

import (
	"strings"
	"testing"
)

// A candidate that stops over its environment says which variable, and a
// connection refused on loopback says which port — never the line itself.
func TestApplicationOutputCauseNamesTheEnvironment(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		line     string
		code     string
		variable string
		port     int
	}{
		{"ArgumentError: Missing `secret_key_base` for 'production' environment, set this string with `bin/rails credentials:edit`", "secret_missing", "SECRET_KEY_BASE", 0},
		{"ActiveSupport::MessageEncryptor::InvalidMessage (ActiveSupport::MessageEncryptor::InvalidMessage)", "master_key_invalid", "RAILS_MASTER_KEY", 0},
		{"django.core.exceptions.ImproperlyConfigured: The SECRET_KEY setting must not be empty.", "secret_missing", "SECRET_KEY", 0},
		{"[auth][error] MissingSecret: Please define a `secret`.", "secret_missing", "AUTH_SECRET", 0},
		{"[auth][error] UntrustedHost: Host must be trusted. URL was: https://app.example.com/api/auth/session", "auth_untrusted_host", "AUTH_URL", 0},
		{"[error] Could not check origin for Phoenix.Socket transport.", "phoenix_origin_rejected", "PHX_HOST", 0},
		{"** (RuntimeError) environment variable DATABASE_URL is missing.", "variable_missing", "DATABASE_URL", 0},
		{"2025/01/01 Error loading .env file", "dotenv_missing", "", 0},
		{"Error: connect ECONNREFUSED 127.0.0.1:5432", "loopback_refused", "", 5432},
		{"dial tcp [::1]:6379: connect: connection refused", "loopback_refused", "", 6379},
		{`connection to server at "localhost" (127.0.0.1), port 5432 failed: Connection refused`, "loopback_refused", "", 5432},
	} {
		cause := applicationOutputCause([]ContainerDiagnostics{{Lines: []RuntimeLogLine{{Text: test.line}}}})
		if cause == nil || cause.Code != test.code || cause.Variable != test.variable || cause.Port != test.port {
			t.Fatalf("%q = %+v", test.line, cause)
		}
		sentence := cause.sentence()
		if sentence == "" || strings.Contains(sentence, test.line) {
			t.Fatalf("%q sentence = %q", test.line, sentence)
		}
		if err := rejectPlanSecretLiteral("cause", sentence); err != nil {
			t.Fatalf("%q sentence is refused: %v", test.line, err)
		}
	}
	if cause := applicationOutputCause([]ContainerDiagnostics{{Lines: []RuntimeLogLine{{Text: "connect ECONNREFUSED 10.0.0.5:5432"}}}}); cause != nil {
		t.Fatalf("a remote refusal is not loopback: %+v", cause)
	}
}
