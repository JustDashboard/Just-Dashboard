package dbx

import "testing"

// Every PostgreSQL session the dashboard opens gives the dashboard's name, so
// the server's session list can tell it from an application's, and a string
// that already names an application is left as it was saved.
func TestPostgresConnectionsGiveTheDashboardsName(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://u:p@127.0.0.1:5432/app":                  "postgres://u:p@127.0.0.1:5432/app?application_name=just-dashboard",
		"postgresql://u:p@db/app?sslmode=disable":            "postgresql://u:p@db/app?sslmode=disable&application_name=just-dashboard",
		"host=127.0.0.1 user=u dbname=app":                   "host=127.0.0.1 user=u dbname=app application_name=just-dashboard",
		"postgres://u:p@db/app?application_name=reporting":   "postgres://u:p@db/app?application_name=reporting",
		"host=db user=u fallback_application_name=reporting": "host=db user=u fallback_application_name=reporting",
		"": "",
	} {
		if got := (postgresDialect{}).NormaliseDSN(in); got != want {
			t.Errorf("NormaliseDSN(%q) = %q, want %q", in, got, want)
		}
	}
}
