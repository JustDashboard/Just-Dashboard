package deploy

import (
	"strings"
	"testing"
)

func TestApplicationOutputCauseNamesTheMissingTable(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"The table `public.products` does not exist in the current database.": "public.products",
		`ERROR: relation "orders" does not exist`:                             "orders",
		`Error: Table 'shop.customers' doesn't exist`:                         "shop.customers",
		"SqliteError: no such table: sessions":                                "sessions",
	}
	for text, table := range cases {
		cause := applicationOutputCause([]ContainerDiagnostics{{Lines: []RuntimeLogLine{
			{Stream: "stdout", Text: "> next start"},
			{Stream: "stderr", Text: text},
		}}})
		if cause == nil || cause.Code != "schema_missing" || cause.Table != table {
			t.Fatalf("%q diagnosed as %+v", text, cause)
		}
		if sentence := cause.sentence(); !strings.Contains(sentence, "table "+table+" does not exist") || !strings.Contains(sentence, "prisma migrate deploy") {
			t.Fatalf("sentence for %q = %q", text, sentence)
		}
	}
}

func TestApplicationOutputCauseStaysSilentWithoutEvidence(t *testing.T) {
	t.Parallel()
	cause := applicationOutputCause([]ContainerDiagnostics{{Lines: []RuntimeLogLine{
		{Stream: "stderr", Text: "Error: connect ECONNREFUSED 10.0.0.2:5432"},
		{Stream: "stderr", Text: "Error: DATABASE_URL is not set"},
	}}})
	if cause != nil {
		t.Fatalf("unrelated output diagnosed as %+v", cause)
	}
	if got := diagnosticsSuffix(&runtimeDiagnosticsEvidence{Available: true, Lines: 2}); got != "; the application's last output is in the build log" {
		t.Fatalf("suffix without a cause = %q", got)
	}
	if got := diagnosticsSuffix(&runtimeDiagnosticsEvidence{Available: true}); got != "" {
		t.Fatalf("suffix without output = %q", got)
	}
	if got := (&OutputCause{Code: "unknown"}).sentence(); got != "" {
		t.Fatalf("unknown cause sentence = %q", got)
	}
}
