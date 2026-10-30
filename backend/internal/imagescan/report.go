// Package imagescan reads a container image for known vulnerabilities with
// Trivy, run as a container the dashboard owns: offline, unprivileged, with
// no network at all and nothing of the host's but the image it is handed and
// the vulnerability database the dashboard downloaded for it.
//
// The package has three parts. report.go reads Trivy's JSON report into the
// findings a page and an alert need — counts by severity, the fixable ones
// with the version that fixes them — and nothing else. database.go fetches
// Trivy's database from its registry over plain HTTP, in this process, and
// copies it into a managed volume, so the scanner never needs a network.
// scanner.go runs the scan.
package imagescan

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Severities, as Trivy names them, from the worst down.
const (
	SeverityCritical = "CRITICAL"
	SeverityHigh     = "HIGH"
	SeverityMedium   = "MEDIUM"
	SeverityLow      = "LOW"
	SeverityUnknown  = "UNKNOWN"
)

// Severities lists every severity from the worst down; a floor is one of the
// first four.
var Severities = []string{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityUnknown}

// SeverityRank orders severities: a higher number is worse, and a name Trivy
// did not use ranks as unknown.
func SeverityRank(severity string) int {
	switch strings.ToUpper(strings.TrimSpace(severity)) {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	}
	return 0
}

// ValidFloor reports whether a severity may be a floor. Unknown is not one:
// a finding nobody has rated cannot be the line a reader asked to be told at.
func ValidFloor(severity string) bool {
	return SeverityRank(severity) > 0
}

// AtLeast reports whether severity is at or above floor.
func AtLeast(severity, floor string) bool {
	return SeverityRank(severity) >= SeverityRank(floor) && SeverityRank(severity) > 0
}

// Finding is one vulnerability in one package of one image.
type Finding struct {
	ID        string `json:"id"`
	Package   string `json:"package"`
	Installed string `json:"installed"`
	// Fixed is the version that fixes it, as Trivy gives it — sometimes a
	// list ("1.2.3, 1.3.1") — and empty when no fix has been published.
	Fixed    string `json:"fixed,omitempty"`
	Severity string `json:"severity"`
	Title    string `json:"title,omitempty"`
	URL      string `json:"url,omitempty"`
	// Target is the part of the image it was found in: the OS packages or a
	// language lock file.
	Target string `json:"target"`
	// Class is Trivy's: os-pkgs or lang-pkgs.
	Class string `json:"class,omitempty"`
	// FromBase says the same vulnerability in the same package at the same
	// version is in one of the base images the release was built from, so a
	// newer base may be what fixes it.
	FromBase bool `json:"fromBase,omitempty"`
}

// Fixable reports whether a fixed version has been published.
func (f Finding) Fixable() bool { return strings.TrimSpace(f.Fixed) != "" }

// key is what makes two findings the same one: the vulnerability, the
// package and the installed version. The target is left out, because an
// image and its base name their OS packages under different targets.
func (f Finding) key() string { return f.ID + "\x00" + f.Package + "\x00" + f.Installed }

// Report is one image's scan, read from Trivy's JSON.
type Report struct {
	// Scanner is Trivy's version, as the report states it.
	Scanner string `json:"scanner,omitempty"`
	// OS is the distribution the scanner recognised ("alpine 3.12.12"), and
	// EndOfLife says that distribution no longer gets security fixes, which
	// is worth more than any count when it is true.
	OS        string    `json:"os,omitempty"`
	EndOfLife bool      `json:"endOfLife,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	Findings  []Finding `json:"findings"`
}

// trivyReport is the part of Trivy's JSON report (SchemaVersion 2) this
// package reads. Everything else — packages, layers, CVSS vectors — is left
// on the floor.
type trivyReport struct {
	SchemaVersion int `json:"SchemaVersion"`
	Trivy         struct {
		Version string `json:"Version"`
	} `json:"Trivy"`
	CreatedAt time.Time `json:"CreatedAt"`
	Metadata  struct {
		OS *struct {
			Family string `json:"Family"`
			Name   string `json:"Name"`
			EOSL   bool   `json:"EOSL"`
		} `json:"OS"`
	} `json:"Metadata"`
	Results []struct {
		Target          string `json:"Target"`
		Class           string `json:"Class"`
		Vulnerabilities []struct {
			VulnerabilityID  string `json:"VulnerabilityID"`
			PkgName          string `json:"PkgName"`
			InstalledVersion string `json:"InstalledVersion"`
			FixedVersion     string `json:"FixedVersion"`
			Severity         string `json:"Severity"`
			Title            string `json:"Title"`
			PrimaryURL       string `json:"PrimaryURL"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

// ErrReport is a report that is not Trivy's JSON in the shape this package
// was written against.
var ErrReport = errors.New("the scanner's report could not be read")

// ParseReport reads Trivy's JSON report. A report of another schema version
// is refused rather than read as clean: a field that moved would otherwise
// read as an image with no findings.
func ParseReport(raw []byte) (*Report, error) {
	var parsed trivyReport
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReport, err)
	}
	if parsed.SchemaVersion != 2 {
		return nil, fmt.Errorf("%w: schema version %d, expected 2", ErrReport, parsed.SchemaVersion)
	}
	report := &Report{Scanner: parsed.Trivy.Version, CreatedAt: parsed.CreatedAt, Findings: []Finding{}}
	if os := parsed.Metadata.OS; os != nil {
		report.OS = strings.TrimSpace(os.Family + " " + os.Name)
		report.EndOfLife = os.EOSL
	}
	seen := map[string]bool{}
	for _, result := range parsed.Results {
		for _, v := range result.Vulnerabilities {
			finding := Finding{
				ID: strings.TrimSpace(v.VulnerabilityID), Package: strings.TrimSpace(v.PkgName),
				Installed: strings.TrimSpace(v.InstalledVersion), Fixed: strings.TrimSpace(v.FixedVersion),
				Severity: normalSeverity(v.Severity), Title: clip(strings.TrimSpace(v.Title), 200),
				URL: safeURL(v.PrimaryURL), Target: targetName(result.Target), Class: result.Class,
			}
			if finding.ID == "" || finding.Package == "" {
				continue
			}
			// The same package can be reported twice when two lock files
			// name it; it is one thing to fix.
			if seen[finding.key()] {
				continue
			}
			seen[finding.key()] = true
			report.Findings = append(report.Findings, finding)
		}
	}
	sortFindings(report.Findings)
	return report, nil
}

// targetName drops the archive's path from Trivy's target, which names the
// file it was handed rather than anything in the image: "/scan/image.tar
// (alpine 3.12.12)" is the OS packages of alpine 3.12.12.
func targetName(target string) string {
	if rest, ok := strings.CutPrefix(target, ArchivePath); ok {
		target = strings.TrimSpace(rest)
		target = strings.TrimSuffix(strings.TrimPrefix(target, "("), ")")
	}
	if target == "" {
		target = "image"
	}
	return clip(target, 200)
}

func normalSeverity(severity string) string {
	severity = strings.ToUpper(strings.TrimSpace(severity))
	if SeverityRank(severity) == 0 {
		return SeverityUnknown
	}
	return severity
}

// safeURL keeps an advisory's address only when it is an https one: it is
// drawn as a link, and a report is data from a database the dashboard
// downloaded, not something to hand a browser unread.
func safeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "https://") || strings.ContainsAny(raw, " \"'<>\x00\r\n") || len(raw) > 300 {
		return ""
	}
	return raw
}

func clip(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.TrimRight(value[:limit], " ") + "…"
}

// sortFindings puts the worst first, the fixable before the unfixable at the
// same severity, then by package and id, so a list and a test read the same.
func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if SeverityRank(a.Severity) != SeverityRank(b.Severity) {
			return SeverityRank(a.Severity) > SeverityRank(b.Severity)
		}
		if a.Fixable() != b.Fixable() {
			return a.Fixable()
		}
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		return a.ID < b.ID
	})
}

// MarkFromBase marks each finding of an image that one of its base images
// carries too: the same vulnerability in the same package at the same
// version. What the base carries a newer base may fix; what it does not came
// with the application.
func MarkFromBase(findings []Finding, bases ...[]Finding) []Finding {
	inBase := map[string]bool{}
	for _, base := range bases {
		for _, finding := range base {
			inBase[finding.key()] = true
		}
	}
	out := make([]Finding, len(findings))
	for i, finding := range findings {
		finding.FromBase = inBase[finding.key()]
		out[i] = finding
	}
	return out
}

// Counts is how many findings there are at each severity.
type Counts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Unknown  int `json:"unknown"`
}

// Total is every finding counted.
func (c Counts) Total() int { return c.Critical + c.High + c.Medium + c.Low + c.Unknown }

func (c *Counts) add(severity string) {
	switch normalSeverity(severity) {
	case SeverityCritical:
		c.Critical++
	case SeverityHigh:
		c.High++
	case SeverityMedium:
		c.Medium++
	case SeverityLow:
		c.Low++
	default:
		c.Unknown++
	}
}

// AtLeast is how many are at or above floor.
func (c Counts) AtLeast(floor string) int {
	total := 0
	for severity, count := range map[string]int{
		SeverityCritical: c.Critical, SeverityHigh: c.High, SeverityMedium: c.Medium, SeverityLow: c.Low,
	} {
		if AtLeast(severity, floor) {
			total += count
		}
	}
	return total
}

// Summary is what a release's scan keeps: every finding counted, every
// finding and every fixable one at or above the floor listed, each list
// bounded so a release built on an old distribution does not write a
// thousand rows a page then has to draw.
type Summary struct {
	Counts Counts `json:"counts"`
	// FixableCounts counts only the findings with a published fix.
	FixableCounts Counts `json:"fixableCounts"`
	// Fixable is the fixable findings at or above the floor, worst first.
	Fixable []Finding `json:"fixable"`
	// Unfixed is the findings at or above the floor that nobody has
	// published a fix for: nothing a rebuild can do about, and worth saying.
	Unfixed []Finding `json:"unfixed"`
	// Truncated says either list was cut at its bound.
	Truncated bool `json:"truncated,omitempty"`
}

// MaxListed bounds each list a summary keeps.
const MaxListed = 200

// Summarize counts findings and lists those at or above floor. An invalid
// floor is HIGH, the default.
func Summarize(findings []Finding, floor string) Summary {
	if !ValidFloor(floor) {
		floor = SeverityHigh
	}
	summary := Summary{Fixable: []Finding{}, Unfixed: []Finding{}}
	sorted := append([]Finding(nil), findings...)
	sortFindings(sorted)
	for _, finding := range sorted {
		summary.Counts.add(finding.Severity)
		if finding.Fixable() {
			summary.FixableCounts.add(finding.Severity)
		}
		if !AtLeast(finding.Severity, floor) {
			continue
		}
		list := &summary.Unfixed
		if finding.Fixable() {
			list = &summary.Fixable
		}
		if len(*list) >= MaxListed {
			summary.Truncated = true
			continue
		}
		*list = append(*list, finding)
	}
	return summary
}

// FixableAtLeast reports whether a summary lists a fixable finding at or
// above floor: the half of the alert that is about the image.
func (s Summary) FixableAtLeast(floor string) bool {
	for _, finding := range s.Fixable {
		if AtLeast(finding.Severity, floor) {
			return true
		}
	}
	return false
}
