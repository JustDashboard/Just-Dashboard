// Package netipam plans shared address space without changing any native
// manager. Inventory omissions are coverage evidence, never free space.
package netipam

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxPools = 64
const MaxReservations = 2048
const MaxObservations = 4096
const ReleasedRetention = 30 * 24 * time.Hour

var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type PoolRequest struct {
	Name           string `json:"name"`
	Prefix         string `json:"prefix"`
	AllocationBits int    `json:"allocationBits"`
}
type Pool struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Prefix         string     `json:"prefix"`
	Family         string     `json:"family"`
	AllocationBits int        `json:"allocationBits"`
	CreatedAt      time.Time  `json:"createdAt"`
	RetiredAt      *time.Time `json:"retiredAt,omitempty"`
}
type ReserveRequest struct {
	PoolID             string `json:"poolId"`
	Prefix             string `json:"prefix,omitempty"`
	Owner              string `json:"owner"`
	Resource           string `json:"resource"`
	AcknowledgeUnknown bool   `json:"acknowledgeUnknown"`
}
type Reservation struct {
	ID                  string     `json:"id"`
	PoolID              string     `json:"poolId"`
	Prefix              string     `json:"prefix"`
	Family              string     `json:"family"`
	Owner               string     `json:"owner"`
	Resource            string     `json:"resource"`
	State               string     `json:"state"`
	UnknownSources      []string   `json:"unknownSources"`
	AcknowledgedUnknown bool       `json:"acknowledgedUnknown"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	ReleasedAt          *time.Time `json:"releasedAt,omitempty"`
	StartedBy           string     `json:"startedBy"`
	NativeID            string     `json:"nativeId,omitempty"`
	Detail              string     `json:"detail,omitempty"`
}
type Observation struct {
	Prefix   string `json:"prefix"`
	Owner    string `json:"owner"`
	Resource string `json:"resource"`
	Domain   string `json:"domain"`
	Basis    string `json:"basis"`
}
type Coverage struct {
	Source    string    `json:"source"`
	State     string    `json:"state"`
	Detail    string    `json:"detail"`
	CheckedAt time.Time `json:"checkedAt"`
}
type Snapshot struct {
	CheckedAt    time.Time     `json:"checkedAt"`
	FinishedAt   time.Time     `json:"finishedAt"`
	Observations []Observation `json:"observations"`
	Coverage     []Coverage    `json:"coverage"`
}
type Conflict struct {
	Prefix        string `json:"prefix"`
	Owner         string `json:"owner"`
	Resource      string `json:"resource"`
	Basis         string `json:"basis"`
	ReservationID string `json:"reservationId,omitempty"`
	Domain        string `json:"domain,omitempty"`
}
type Preview struct {
	Prefix      string     `json:"prefix"`
	Status      string     `json:"status"`
	Conflicts   []Conflict `json:"conflicts"`
	Coverage    []Coverage `json:"coverage"`
	CheckedAt   time.Time  `json:"checkedAt"`
	Limitations []string   `json:"limitations"`
}
type Utilization struct {
	PoolID            string `json:"poolId"`
	TotalAddresses    string `json:"totalAddresses"`
	TotalBlocks       string `json:"totalBlocks"`
	ReservedBlocks    string `json:"reservedBlocks"`
	ObservedBlocks    string `json:"observedBlocks"`
	UnavailableBlocks string `json:"unavailableBlocks"`
	CandidateBlocks   string `json:"candidateBlocks"`
	Coverage          string `json:"coverage"`
}
type View struct {
	Pools        []Pool        `json:"pools"`
	Reservations []Reservation `json:"reservations"`
	Inventory    Snapshot      `json:"inventory"`
	Utilization  []Utilization `json:"utilization"`
	Limitations  []string      `json:"limitations"`
}
type Handoff struct {
	ID             string
	ReservationIDs []string
	Owner          string
	Resource       string
}

func Family(p netip.Prefix) string {
	if p.Addr().Is4() {
		return "inet"
	}
	return "inet6"
}
func Canonical(value string) (netip.Prefix, error) {
	p, e := netip.ParsePrefix(strings.TrimSpace(value))
	if e != nil || p.Addr().Is4In6() || p.Addr().Zone() != "" || p != p.Masked() {
		return netip.Prefix{}, fmt.Errorf("provide a canonical IPv4 or IPv6 network prefix")
	}
	return p, nil
}
func ValidatePool(in PoolRequest) (PoolRequest, error) {
	if !utf8.ValidString(in.Name) || strings.IndexFunc(in.Name, unicode.IsControl) >= 0 {
		return in, fmt.Errorf("pool names cannot contain control characters or invalid UTF-8")
	}
	in.Name = strings.TrimSpace(in.Name)
	p, e := Canonical(in.Prefix)
	if e != nil {
		return in, e
	}
	if len(in.Name) < 1 || len(in.Name) > 80 || !utf8.ValidString(in.Name) || strings.IndexFunc(in.Name, unicode.IsControl) >= 0 || in.AllocationBits < p.Bits() || in.AllocationBits > p.Addr().BitLen() {
		return in, fmt.Errorf("provide a pool name and an allocation prefix inside its family width")
	}
	if p.Addr().IsMulticast() || p.Addr().IsLoopback() || p.Addr().IsLinkLocalUnicast() {
		return in, fmt.Errorf("a shared pool cannot be multicast, loopback or link-local")
	}
	in.Prefix = p.String()
	return in, nil
}
func ValidateOwner(owner, resource string) error {
	switch owner {
	case "docker_network", "wireguard_server", "interface_address", "namespace_address", "provider_range":
	default:
		return fmt.Errorf("select a supported intended owner")
	}
	if !namePattern.MatchString(resource) {
		return fmt.Errorf("provide the exact intended native resource name")
	}
	return nil
}
func unknown(snapshot Snapshot) bool {
	for _, v := range snapshot.Coverage {
		if v.State != "observed" {
			return true
		}
	}
	return len(snapshot.Coverage) == 0
}

var limitations = []string{
	"Reservations hold planning space only; they do not reserve or adopt native kernel, Docker, VPN or provider resources.",
	"Known overlap is a snapshot. Provider allocations, remote/foreign namespaces and unsupported native plugins remain unknown until authoritative owners supply them.",
	"A creation handoff rechecks current evidence through its existing native owner. Foreign writers can still change state after that check.",
	"Named namespace overlaps can be intentional isolation. Shared allocation conservatively avoids their observed ranges without inferring a connected packet path.",
}

func unknownSources(snapshot Snapshot) []string {
	sources := []string{}
	for _, v := range snapshot.Coverage {
		if v.State != "observed" {
			sources = append(sources, v.Source+":"+v.State)
		}
	}
	if len(snapshot.Coverage) == 0 {
		sources = append(sources, "inventory:unknown")
	}
	sort.Strings(sources)
	return sources
}
func acknowledgedCoverage(current Snapshot, saved []string) bool {
	approved := map[string]bool{}
	for _, v := range saved {
		approved[v] = true
	}
	for _, v := range unknownSources(current) {
		if !approved[v] {
			return false
		}
	}
	return true
}
