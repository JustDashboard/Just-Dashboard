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

// A schema step that pushes the declared model refuses a change that would
// drop or rename data, or stops to ask about it, and the start command never
// reaches the server.
var schemaPushRefusedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`Use the --accept-data-loss flag`),                         // prisma db push
	regexp.MustCompile(`We found changes that cannot be executed`),                // prisma db push
	regexp.MustCompile(`created or renamed from another (?:column|table)`),        // drizzle-kit push
	regexp.MustCompile(`THIS ACTION WILL CAUSE DATA LOSS AND CANNOT BE REVERTED`), // drizzle-kit push
	regexp.MustCompile(`Interactive prompts require a TTY terminal`),              // drizzle-kit push
}

// A SQLite file in a directory the container's user cannot write.
var sqliteReadOnlyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`attempt to write a readonly database`),
	regexp.MustCompile(`SQLITE_READONLY`),
	regexp.MustCompile(`SQLITE_CANTOPEN|unable to open database file`),
}

func applicationOutputCause(containers []ContainerDiagnostics) *OutputCause {
	for _, container := range containers {
		for _, line := range container.Lines {
			for _, pattern := range missingTablePatterns {
				if match := pattern.FindStringSubmatch(line.Text); match != nil {
					return &OutputCause{Code: "schema_missing", Table: match[1]}
				}
			}
			for _, pattern := range schemaPushRefusedPatterns {
				if pattern.MatchString(line.Text) {
					return &OutputCause{Code: "schema_push_refused"}
				}
			}
			for _, pattern := range sqliteReadOnlyPatterns {
				if pattern.MatchString(line.Text) {
					return &OutputCause{Code: "sqlite_not_writable"}
				}
			}
		}
	}
	return nil
}

func (c *OutputCause) sentence() string {
	if c == nil {
		return ""
	}
	switch c.Code {
	case "schema_missing":
		return fmt.Sprintf("the application reports that table %s does not exist in its database, so the linked database has not received the application's schema; apply it before the application starts — for Prisma, `prisma migrate deploy`, or `prisma db push` when the project has no migrations", c.Table)
	case "schema_push_refused":
		return "the start command's schema push refused a change that would drop or rename data (or stopped to ask about it), so the server never started; commit migrations so the start applies them — `prisma migrate dev` then `prisma migrate deploy`, or `drizzle-kit generate` then `drizzle-kit migrate` — or apply the change to the database by hand"
	case "sqlite_not_writable":
		return "the application cannot write its SQLite database file, because the directory holding it is not writable by the container's user; keep the file in the image's data directory or on a volume that user owns"
	}
	return ""
}

// diagnosticsSuffix is what the candidate's own output adds to a failure
// message: the one cause it proves, if any, and where the rest of it is.
func diagnosticsSuffix(diagnostics *runtimeDiagnosticsEvidence) string {
	if diagnostics == nil || diagnostics.Lines == 0 {
		return ""
	}
	suffix := "; the application's last output is in the build log"
	if cause := diagnostics.Cause.sentence(); cause != "" {
		suffix = "; " + cause + suffix
	}
	return suffix
}
