package imagescan

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The two fixtures are Trivy 0.76.1's own reports of alpine:3.12 and
// alpine:3.22, read through the scanner container this package runs, with
// the package lists and layers cut out.
func TestTrivysReportOfAnOldAlpineIsReadIntoItsFinding(t *testing.T) {
	report, err := ParseReport(readFixture(t, "trivy-alpine-3.12.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanner != "0.76.1" || report.OS != "alpine 3.12.12" || !report.EndOfLife {
		t.Fatalf("the report's scanner and distribution = %q %q eol=%v", report.Scanner, report.OS, report.EndOfLife)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %+v", report.Findings)
	}
	zlib := report.Findings[0]
	if zlib.ID != "CVE-2022-37434" || zlib.Package != "zlib" || zlib.Installed != "1.2.12-r0" ||
		zlib.Fixed != "1.2.12-r2" || zlib.Severity != SeverityCritical || !zlib.Fixable() {
		t.Fatalf("zlib = %+v", zlib)
	}
	// The archive's path is the scanner's, not the image's.
	if zlib.Target != "alpine 3.12.12" || zlib.Class != "os-pkgs" {
		t.Fatalf("target = %q class = %q", zlib.Target, zlib.Class)
	}
	if !strings.HasPrefix(zlib.URL, "https://avd.aquasec.com/") || zlib.Title == "" {
		t.Fatalf("advisory = %q %q", zlib.URL, zlib.Title)
	}

	clean, err := ParseReport(readFixture(t, "trivy-alpine-3.22.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(clean.Findings) != 0 || clean.OS != "alpine 3.22.7" || clean.EndOfLife {
		t.Fatalf("a clean image read as %+v", clean)
	}
}

func TestAReportOfAnotherShapeIsRefusedRatherThanReadAsClean(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":       "Trivy said nothing useful",
		"schema 1":       `{"SchemaVersion":1,"Results":[]}`,
		"schema missing": `{"Results":[{"Target":"x","Vulnerabilities":[]}]}`,
	} {
		if _, err := ParseReport([]byte(raw)); !errors.Is(err, ErrReport) {
			t.Errorf("%s was read: %v", name, err)
		}
	}
}

func TestReportFindingsAreDedupedSortedAndTheirLinksKeptOnlyWhenSafe(t *testing.T) {
	raw := `{"SchemaVersion":2,"Trivy":{"Version":"0.76.1"},"Results":[
	  {"Target":"/scan/image.tar (debian 12.7)","Class":"os-pkgs","Vulnerabilities":[
	    {"VulnerabilityID":"CVE-1","PkgName":"openssl","InstalledVersion":"3.0.1","FixedVersion":"3.0.2","Severity":"HIGH","PrimaryURL":"javascript:alert(1)"},
	    {"VulnerabilityID":"CVE-2","PkgName":"bash","InstalledVersion":"5.1","Severity":"CRITICAL","Status":"affected"},
	    {"VulnerabilityID":"CVE-3","PkgName":"curl","InstalledVersion":"7.0","FixedVersion":"7.1","Severity":"critical","PrimaryURL":"https://example.test/CVE-3"},
	    {"VulnerabilityID":"CVE-4","PkgName":"tar","InstalledVersion":"1.0","FixedVersion":"1.1","Severity":"weird"}
	  ]},
	  {"Target":"app/package-lock.json","Class":"lang-pkgs","Vulnerabilities":[
	    {"VulnerabilityID":"CVE-5","PkgName":"lodash","InstalledVersion":"4.17.20","FixedVersion":"4.17.21","Severity":"MEDIUM"},
	    {"VulnerabilityID":"CVE-5","PkgName":"lodash","InstalledVersion":"4.17.20","FixedVersion":"4.17.21","Severity":"MEDIUM"},
	    {"VulnerabilityID":"","PkgName":"nameless","InstalledVersion":"1"}
	  ]}
	]}`
	report, err := ParseReport([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, finding := range report.Findings {
		order = append(order, finding.ID)
	}
	// Worst first; at one severity the fixable before the unfixable.
	if strings.Join(order, ",") != "CVE-3,CVE-2,CVE-1,CVE-5,CVE-4" {
		t.Fatalf("order = %v", order)
	}
	if report.Findings[0].URL != "https://example.test/CVE-3" || report.Findings[2].URL != "" {
		t.Fatalf("a javascript: advisory link survived: %+v", report.Findings[2])
	}
	if report.Findings[4].Severity != SeverityUnknown || report.Findings[0].Severity != SeverityCritical {
		t.Fatalf("severities were not normalised: %+v", report.Findings)
	}
	if report.Findings[3].Target != "app/package-lock.json" || report.Findings[0].Target != "debian 12.7" {
		t.Fatalf("targets = %q %q", report.Findings[3].Target, report.Findings[0].Target)
	}
}

func TestTheFloorDecidesWhatIsListedAndEveryFindingIsCounted(t *testing.T) {
	findings := []Finding{
		{ID: "C1", Package: "a", Installed: "1", Fixed: "2", Severity: SeverityCritical},
		{ID: "C2", Package: "b", Installed: "1", Severity: SeverityCritical},
		{ID: "H1", Package: "c", Installed: "1", Fixed: "2", Severity: SeverityHigh},
		{ID: "M1", Package: "d", Installed: "1", Fixed: "2", Severity: SeverityMedium},
		{ID: "L1", Package: "e", Installed: "1", Severity: SeverityLow},
		{ID: "U1", Package: "f", Installed: "1", Fixed: "2", Severity: SeverityUnknown},
	}
	high := Summarize(findings, SeverityHigh)
	if high.Counts != (Counts{Critical: 2, High: 1, Medium: 1, Low: 1, Unknown: 1}) || high.Counts.Total() != 6 {
		t.Fatalf("counts = %+v", high.Counts)
	}
	if high.FixableCounts != (Counts{Critical: 1, High: 1, Medium: 1, Unknown: 1}) {
		t.Fatalf("fixable counts = %+v", high.FixableCounts)
	}
	if len(high.Fixable) != 2 || high.Fixable[0].ID != "C1" || high.Fixable[1].ID != "H1" {
		t.Fatalf("fixable at HIGH = %+v", high.Fixable)
	}
	if len(high.Unfixed) != 1 || high.Unfixed[0].ID != "C2" {
		t.Fatalf("unfixed at HIGH = %+v", high.Unfixed)
	}
	if !high.FixableAtLeast(SeverityHigh) || !high.FixableAtLeast(SeverityCritical) {
		t.Fatal("a fixable critical is fixable at the high and critical floors")
	}

	critical := Summarize(findings, SeverityCritical)
	if len(critical.Fixable) != 1 || critical.Fixable[0].ID != "C1" {
		t.Fatalf("fixable at CRITICAL = %+v", critical.Fixable)
	}
	if critical.Counts.AtLeast(SeverityCritical) != 2 || high.Counts.AtLeast(SeverityHigh) != 3 || high.Counts.AtLeast(SeverityLow) != 5 {
		t.Fatalf("at-least counts = %d %d %d", critical.Counts.AtLeast(SeverityCritical), high.Counts.AtLeast(SeverityHigh), high.Counts.AtLeast(SeverityLow))
	}
	// An unknown severity is never at a floor, so nobody is told about a
	// finding nobody rated.
	low := Summarize(findings, SeverityLow)
	for _, finding := range low.Fixable {
		if finding.Severity == SeverityUnknown {
			t.Fatalf("an unrated finding was listed at the lowest floor: %+v", finding)
		}
	}
	if invalid := Summarize(findings, "NOPE"); len(invalid.Fixable) != 2 {
		t.Fatalf("an invalid floor is HIGH: %+v", invalid.Fixable)
	}
	only := Summarize([]Finding{{ID: "M", Package: "x", Installed: "1", Fixed: "2", Severity: SeverityMedium}}, SeverityHigh)
	if only.FixableAtLeast(SeverityHigh) || len(only.Fixable) != 0 || only.FixableCounts.Medium != 1 {
		t.Fatalf("a fixable medium is not a fixable high: %+v", only)
	}
}

func TestSummariesAreBounded(t *testing.T) {
	findings := make([]Finding, 0, MaxListed+5)
	for i := 0; i < MaxListed+5; i++ {
		findings = append(findings, Finding{ID: "CVE", Package: strings.Repeat("p", i+1), Installed: "1", Fixed: "2", Severity: SeverityHigh})
	}
	summary := Summarize(findings, SeverityHigh)
	if len(summary.Fixable) != MaxListed || !summary.Truncated || summary.Counts.High != MaxListed+5 {
		t.Fatalf("listed %d, truncated %v, counted %d", len(summary.Fixable), summary.Truncated, summary.Counts.High)
	}
}

func TestAFindingTheBaseCarriesIsMarkedAsFromTheBase(t *testing.T) {
	image := []Finding{
		{ID: "CVE-1", Package: "openssl", Installed: "3.0.1", Target: "alpine 3.19.1"},
		{ID: "CVE-2", Package: "lodash", Installed: "4.17.20", Target: "app/package-lock.json"},
		{ID: "CVE-1", Package: "openssl", Installed: "3.0.9", Target: "alpine 3.19.1"},
	}
	base := []Finding{{ID: "CVE-1", Package: "openssl", Installed: "3.0.1", Target: "alpine 3.19.0"}}
	marked := MarkFromBase(image, base)
	if !marked[0].FromBase || marked[1].FromBase || marked[2].FromBase {
		t.Fatalf("marks = %v %v %v", marked[0].FromBase, marked[1].FromBase, marked[2].FromBase)
	}
	if image[0].FromBase {
		t.Fatal("the image's own findings were changed in place")
	}
}
