package deploy

import (
	"fmt"
	"regexp"
)

// OutputCause is the one thing a candidate's own output proves about why its
// checks failed. It carries an identifier and the object it names, never a
// line of output, so step evidence stays free of whatever else the
// application printed alongside it.
type OutputCause struct {
	Code  string `json:"code"`
	Table string `json:"table,omitempty"`
	// Listener is the loopback address a loopback_only candidate listens on.
	Listener string `json:"listener,omitempty"`
}

// A freshly linked database is empty, and an application that queries it
// before anything applied its schema fails every request with one of these.
// Each pattern captures the table so the message can name it.
var missingTablePatterns = []*regexp.Regexp{
	regexp.MustCompile("The table `([^`]+)` does not exist in the current database"), // Prisma P2021
	regexp.MustCompile(`relation "([^"]+)" does not exist`),                          // PostgreSQL 42P01
	regexp.MustCompile(`Table '([^']+)' doesn't exist`),                              // MySQL and MariaDB 1146
	regexp.MustCompile(`no such table: ([A-Za-z0-9_.]+)`),                            // SQLite
}

func applicationOutputCause(containers []ContainerDiagnostics) *OutputCause {
	for _, container := range containers {
		for _, line := range container.Lines {
			for _, pattern := range missingTablePatterns {
				if match := pattern.FindStringSubmatch(line.Text); match != nil {
					return &OutputCause{Code: "schema_missing", Table: match[1]}
				}
			}
		}
	}
	return startCommandExitedCause(containers)
}

func (c *OutputCause) sentence() string {
	if c == nil {
		return ""
	}
	switch c.Code {
	case "start_command_exited":
		return "the application's start command finished with exit code 0 instead of serving, so the container stopped; a start command must keep the server in the foreground — pm2 start, forever start, a trailing & and a one-off script all return at once (use pm2-runtime, or run the server directly)"
	case "schema_missing":
		return fmt.Sprintf("the application reports that table %s does not exist in its database, so the linked database has not received the application's schema; apply it before the application starts — for Prisma, `prisma migrate deploy`, or `prisma db push` when the project has no migrations", c.Table)
	case "loopback_only":
		return loopbackOnlySentence(c.Listener)
	}
	return ""
}

// diagnosticsSuffix is what the candidate's own output adds to a failure
// message: the one cause it proves, if any, and where the rest of it is.
func diagnosticsSuffix(diagnostics *runtimeDiagnosticsEvidence) string {
	if diagnostics == nil {
		return ""
	}
	suffix := ""
	if diagnostics.Lines > 0 {
		suffix = "; the application's last output is in the build log"
	}
	// A server listening on loopback often prints nothing about it, so its
	// cause stands without any output behind it.
	if cause := diagnostics.Cause.sentence(); cause != "" {
		suffix = "; " + cause + suffix
	}
	return suffix
}
