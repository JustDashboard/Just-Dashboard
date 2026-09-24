package deploy

import (
	"fmt"
	"regexp"
	"strconv"
)

// The environment causes a candidate's own output can prove: a secret or
// variable the application stops without, credentials it cannot decrypt, an
// origin it refuses, and a connection to loopback — which inside the
// container is the application itself. Each names the variable or port, never
// the line it was read from.
var environmentCausePatterns = []struct {
	pattern  *regexp.Regexp
	code     string
	variable string
}{
	{regexp.MustCompile("Missing .?secret_key_base.? for '[a-z]+' environment"), "secret_missing", "SECRET_KEY_BASE"},
	{regexp.MustCompile(`ActiveSupport::MessageEncryptor::InvalidMessage`), "master_key_invalid", "RAILS_MASTER_KEY"},
	{regexp.MustCompile(`The SECRET_KEY setting must not be empty`), "secret_missing", "SECRET_KEY"},
	{regexp.MustCompile(`\[auth\]\[error\] MissingSecret|MissingSecret: Please define a .secret.`), "secret_missing", "AUTH_SECRET"},
	{regexp.MustCompile(`\[auth\]\[error\] UntrustedHost|UntrustedHost: Host must be trusted`), "auth_untrusted_host", "AUTH_URL"},
	{regexp.MustCompile(`Could not check origin for Phoenix\.Socket transport`), "phoenix_origin_rejected", "PHX_HOST"},
	{regexp.MustCompile(`environment variable ([A-Z][A-Z0-9_]+) is missing`), "variable_missing", ""},
	{regexp.MustCompile(`open \.env: no such file or directory|Error loading \.env file|\.env file not found`), "dotenv_missing", ""},
}

var loopbackRefusedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`ECONNREFUSED (?:127\.0\.0\.1|::1|localhost):(\d+)`),                              // Node
	regexp.MustCompile(`dial tcp (?:127\.0\.0\.1|\[::1\]|localhost):(\d+): connect: connection refused`), // Go
	regexp.MustCompile(`connection to server at "(?:localhost|127\.0\.0\.1)"[^,]*, port (\d+) failed`),   // libpq
	regexp.MustCompile(`Can't connect to (?:local )?(?:MySQL )?server on '(?:localhost|127\.0\.0\.1)(?::(\d+))?'`),
	regexp.MustCompile(`Connection refused \(os error 111\).*(?:127\.0\.0\.1|localhost):(\d+)`), // Rust
}

func environmentOutputCause(line string) *OutputCause {
	for _, cause := range environmentCausePatterns {
		match := cause.pattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		variable := cause.variable
		if variable == "" && len(match) > 1 {
			variable = match[1]
		}
		return &OutputCause{Code: cause.code, Variable: variable}
	}
	for _, pattern := range loopbackRefusedPatterns {
		if match := pattern.FindStringSubmatch(line); match != nil {
			port, _ := strconv.Atoi(match[1])
			return &OutputCause{Code: "loopback_refused", Port: port}
		}
	}
	return nil
}

func (c *OutputCause) environmentSentence() string {
	switch c.Code {
	case "secret_missing":
		return fmt.Sprintf("the application reports that %s is not set; let the dashboard generate it under the project's Variables, or enter one", c.Variable)
	case "master_key_invalid":
		return "Rails cannot decrypt its committed credentials with RAILS_MASTER_KEY; paste the contents of config/master.key under the project's Variables"
	case "auth_untrusted_host":
		return "Auth.js does not trust the host it is served on; set AUTH_URL to the site's address, or AUTH_TRUST_HOST to true"
	case "phoenix_origin_rejected":
		return "Phoenix refuses LiveView connections from this address; set PHX_HOST to the domain the site is served on"
	case "variable_missing":
		return fmt.Sprintf("the application stops because %s is not set; set it under the project's Variables", c.Variable)
	case "dotenv_missing":
		return "the application exits because it cannot open .env; the dashboard supplies variables in the environment, so create an empty .env in the image or ignore the load error"
	case "loopback_refused":
		return fmt.Sprintf("the application connects to localhost:%d, which inside the container is the application itself; link a database or point the variable at a host the container can reach", c.Port)
	}
	return ""
}
