// Package netcapture retains explicitly requested, bounded packet captures.
package netcapture

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

const (
	JobPrefix        = "network.capture."
	MaxRunning       = 2
	MaxRetained      = 32
	MaxArtifactBytes = 2 * 1024 * 1024
	Retention        = 24 * time.Hour
)

var (
	ErrInvalid        = errors.New("invalid capture request")
	ErrUnavailable    = errors.New("packet capture is unavailable")
	ErrNotFound       = errors.New("packet capture was not found")
	ErrBusy           = errors.New("two captures are already running")
	ErrRunning        = errors.New("stop the capture before deleting it")
	identifierPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
	interfacePattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,14}$`)
)

type Request struct {
	Interface      string `json:"interface"`
	Family         string `json:"family"`
	Protocol       string `json:"protocol"`
	Source         string `json:"source,omitempty"`
	Destination    string `json:"destination,omitempty"`
	Port           int    `json:"port,omitempty"`
	Packets        int    `json:"packets"`
	Seconds        int    `json:"seconds"`
	MaxBytes       int    `json:"maxBytes"`
	SnapshotLength int    `json:"snapshotLength"`
	IncidentRunID  string `json:"incidentRunId,omitempty"`
}

type Run struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Request     Request    `json:"request"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	EndedAt     *time.Time `json:"endedAt,omitempty"`
	CreatedBy   string     `json:"createdBy"`
	JobID       string     `json:"jobId,omitempty"`
	Result      *Result    `json:"result,omitempty"`
	Error       string     `json:"error,omitempty"`
	Limitations []string   `json:"limitations"`
}

type Result struct {
	CheckedAt         time.Time            `json:"checkedAt"`
	Packets           int                  `json:"packets"`
	Bytes             int                  `json:"bytes"`
	LinkType          uint32               `json:"linkType"`
	SHA256            string               `json:"sha256,omitempty"`
	StopReason        string               `json:"stopReason"`
	PartialPacket     bool                 `json:"partialPacket"`
	KernelDropped     *uint64              `json:"kernelDropped,omitempty"`
	NativeSummary     string               `json:"nativeSummary,omitempty"`
	Cleanup           hostexec.GroupResult `json:"cleanup"`
	InterfaceIndex    int                  `json:"interfaceIndex"`
	IdentityVerified  bool                 `json:"identityVerified"`
	ArtifactAvailable bool                 `json:"artifactAvailable"`
	Artifact          []byte               `json:"-"`
}

type progressKey struct{}

func ReportProgress(ctx context.Context, result Result) {
	if report, ok := ctx.Value(progressKey{}).(func(Result)); ok {
		report(result)
	}
}

type Interface struct {
	NativeKey string `json:"-"`
	Name      string `json:"name"`
	Index     int    `json:"index"`
	Up        bool   `json:"up"`
}

func ValidID(id string) bool { return identifierPattern.MatchString(id) }
func Terminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled" || status == "interrupted"
}

func ValidateName(name string) (string, error) {
	if !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return "", fmt.Errorf("%w: use a name without control characters", ErrInvalid)
	}
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 100 {
		return "", fmt.Errorf("%w: use a name of 1 to 100 UTF-8 bytes", ErrInvalid)
	}
	return name, nil
}

func Validate(r Request) (Request, error) {
	invalid := func(message string) (Request, error) { return Request{}, fmt.Errorf("%w: %s", ErrInvalid, message) }
	if !interfacePattern.MatchString(r.Interface) {
		return invalid("select one native interface with a valid name")
	}
	if r.Family != "inet" && r.Family != "inet6" {
		return invalid("select IPv4 or IPv6 explicitly")
	}
	switch r.Protocol {
	case "all", "tcp", "udp":
	case "icmp":
		if r.Family != "inet" {
			return invalid("ICMP requires IPv4")
		}
	case "icmp6":
		if r.Family != "inet6" {
			return invalid("ICMPv6 requires IPv6")
		}
	default:
		return invalid("select all, TCP, UDP, ICMP or ICMPv6")
	}
	for _, value := range []*string{&r.Source, &r.Destination} {
		if *value == "" {
			continue
		}
		a, err := netip.ParseAddr(*value)
		if err != nil || a.Is4In6() || a.Zone() != "" || (r.Family == "inet6") != a.Is6() {
			return invalid("filter addresses must be literals in the selected family without zones or mapped addresses")
		}
		*value = a.String()
	}
	if r.Port < 0 || r.Port > 65535 || r.Port > 0 && r.Protocol != "tcp" && r.Protocol != "udp" {
		return invalid("a port of 1 to 65535 requires TCP or UDP")
	}
	if r.Packets < 1 || r.Packets > 10000 || r.Seconds < 1 || r.Seconds > 120 {
		return invalid("capture 1 to 10000 packets for 1 to 120 seconds")
	}
	if r.MaxBytes < 24+16+r.SnapshotLength || r.MaxBytes > MaxArtifactBytes {
		return invalid("the byte budget must fit one complete packet and be at most 2 MiB")
	}
	switch r.SnapshotLength {
	case 96, 128, 256, 512:
	default:
		return invalid("snapshot length must be 96, 128, 256 or 512 bytes")
	}
	if r.IncidentRunID != "" && !ValidID(r.IncidentRunID) {
		return invalid("select an existing saved diagnostic identity")
	}
	return r, nil
}

// Only canonical literals and closed tokens become a filter. Names, ranges,
// expressions, file paths and extra native flags are outside this vocabulary.
func Filter(r Request) string {
	parts := []string{map[string]string{"inet": "ip", "inet6": "ip6"}[r.Family]}
	if r.Protocol != "all" {
		parts = append(parts, r.Protocol)
	}
	if r.Source != "" {
		parts = append(parts, "src host "+r.Source)
	}
	if r.Destination != "" {
		parts = append(parts, "dst host "+r.Destination)
	}
	if r.Port > 0 {
		parts = append(parts, "port "+strconv.Itoa(r.Port))
	}
	return strings.Join(parts, " and ")
}

func Argv(r Request) []string {
	return []string{"--signal=TERM", "--kill-after=2s", strconv.Itoa(r.Seconds) + "s", "tcpdump", "-nn", "-p", "-U", "-s", strconv.Itoa(r.SnapshotLength), "-c", strconv.Itoa(r.Packets), "-i", r.Interface, "-w", "-", Filter(r)}
}
