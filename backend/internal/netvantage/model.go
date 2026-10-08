// Package netvantage owns optional controlled probes. Its machine identity is
// separate from dashboard users and confers no general feature capability.
package netvantage

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

const MaxBody = 32 << 10
const Retention = 7 * 24 * time.Hour

var identityPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var targetPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)

type Scope struct {
	ID        string   `json:"id"`
	Target    string   `json:"target"`
	Addresses []string `json:"addresses"`
	Ports     []int    `json:"ports"`
	Families  []string `json:"families"`
}
type EnrollmentRequest struct {
	Name      string  `json:"name"`
	Location  string  `json:"location"`
	Placement string  `json:"placement"`
	Scopes    []Scope `json:"scopes"`
}
type Vantage struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Location   string     `json:"location"`
	Placement  string     `json:"placement"`
	Scopes     []Scope    `json:"scopes"`
	CreatedAt  time.Time  `json:"createdAt"`
	EnrolledAt *time.Time `json:"enrolledAt,omitempty"`
	LastSeen   *time.Time `json:"lastSeen,omitempty"`
	LastIP     string     `json:"lastIp,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}
type Enrollment struct {
	Vantage   Vantage   `json:"vantage"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	ServerKey string    `json:"serverKey"`
}
type Claim struct {
	ServerKey string `json:"serverKey"`
	ID        string `json:"id"`
	Token     string `json:"token"`
	PublicKey string `json:"publicKey"`
	Proof     string `json:"proof"`
}
type Manifest struct {
	Vantage   Vantage `json:"vantage"`
	ServerKey string  `json:"serverKey"`
}
type Request struct {
	VantageID string `json:"vantageId"`
	ScopeID   string `json:"scopeId"`
	Family    string `json:"family"`
	Port      int    `json:"port"`
	TLS       bool   `json:"tls"`
}
type Check struct {
	ID          string     `json:"id"`
	VantageID   string     `json:"vantageId"`
	Request     Request    `json:"request"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	LeasedAt    *time.Time `json:"leasedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Result      *Result    `json:"result,omitempty"`
	StartedBy   string     `json:"startedBy"`
}
type Job struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	VantageID string    `json:"vantageId"`
	Nonce     string    `json:"nonce"`
	Request   Request   `json:"request"`
	Scope     Scope     `json:"scope"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type SignedJob struct {
	Job       Job    `json:"job"`
	Signature string `json:"signature"`
}
type Stage struct {
	Name       string    `json:"name"`
	Basis      string    `json:"basis"`
	State      string    `json:"state"`
	StartedAt  time.Time `json:"startedAt"`
	EndedAt    time.Time `json:"endedAt"`
	Detail     string    `json:"detail"`
	DurationMS int64     `json:"durationMs"`
}
type Certificate struct {
	SHA256    string    `json:"sha256"`
	Subject   string    `json:"subject"`
	Issuer    string    `json:"issuer"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type Result struct {
	CheckID       string       `json:"checkId"`
	VantageID     string       `json:"vantageId"`
	Nonce         string       `json:"nonce"`
	Request       Request      `json:"request"`
	Target        string       `json:"target"`
	Addresses     []string     `json:"addresses"`
	Address       string       `json:"address,omitempty"`
	SourceAddress string       `json:"sourceAddress,omitempty"`
	StartedAt     time.Time    `json:"startedAt"`
	EndedAt       time.Time    `json:"endedAt"`
	Stages        []Stage      `json:"stages"`
	Certificate   *Certificate `json:"certificate,omitempty"`
	Limitations   []string     `json:"limitations"`
}
type Signature struct {
	ServerKey string
	ID        string
	Sequence  int64
	Timestamp int64
	Value     string
}

func ValidateEnrollment(in EnrollmentRequest) (EnrollmentRequest, error) {
	in.Name, in.Location = strings.TrimSpace(in.Name), strings.TrimSpace(in.Location)
	if len(in.Name) < 1 || len(in.Name) > 80 || len(in.Location) > 120 || strings.ContainsAny(in.Name+in.Location, "\r\n\x00") || (in.Placement != "external_host" && in.Placement != "controlled_fixture") || len(in.Scopes) < 1 || len(in.Scopes) > 8 {
		return in, fmt.Errorf("provide a name, declared placement and one to eight exact target scopes")
	}
	seen := map[string]bool{}
	for i, s := range in.Scopes {
		s.ID = strings.TrimSpace(s.ID)
		s.Target = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s.Target)), ".")
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`).MatchString(s.ID) || seen[s.ID] || len(s.Addresses) < 1 || len(s.Addresses) > 16 || len(s.Ports) < 1 || len(s.Ports) > 8 || len(s.Families) < 1 || len(s.Families) > 2 {
			return in, fmt.Errorf("each scope needs a unique short ID, explicit addresses, exact ports and families")
		}
		seen[s.ID] = true
		literal, e := netip.ParseAddr(s.Target)
		if e != nil && (!targetPattern.MatchString(s.Target) || strings.Contains(s.Target, "..")) {
			return in, fmt.Errorf("scope target must be one exact DNS name or address")
		}
		if e == nil {
			s.Target = literal.Unmap().String()
		}
		for n, a := range s.Addresses {
			ip, e := netip.ParseAddr(a)
			if e != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || e == nil && literal.IsValid() && ip.Unmap() != literal.Unmap() {
				return in, fmt.Errorf("scope addresses must be explicit unicast addresses matching a literal target")
			}
			s.Addresses[n] = ip.Unmap().String()
		}
		for _, p := range s.Ports {
			if p < 1 || p > 65535 {
				return in, fmt.Errorf("scope ports must be from 1 to 65535")
			}
		}
		for _, f := range s.Families {
			if f != "inet" && f != "inet6" {
				return in, fmt.Errorf("scope families must be inet or inet6")
			}
		}
		sort.Strings(s.Addresses)
		sort.Ints(s.Ports)
		sort.Strings(s.Families)
		s.Addresses = slices.Compact(s.Addresses)
		s.Ports = slices.Compact(s.Ports)
		s.Families = slices.Compact(s.Families)
		for _, family := range s.Families {
			available := false
			for _, value := range s.Addresses {
				ip, _ := netip.ParseAddr(value)
				available = available || ip.Is4() == (family == "inet")
			}
			if !available {
				return in, fmt.Errorf("each declared family needs an approved address")
			}
		}
		in.Scopes[i] = s
	}
	return in, nil
}
func scopeFor(scopes []Scope, r Request) (Scope, error) {
	for _, s := range scopes {
		if s.ID == r.ScopeID {
			port, family := false, false
			for _, p := range s.Ports {
				port = port || p == r.Port
			}
			for _, f := range s.Families {
				family = family || f == r.Family
			}
			if port && family {
				return s, nil
			}
		}
	}
	return Scope{}, fmt.Errorf("the exact target, port and family are outside the enrolled scope")
}
func Marshal(v any) []byte            { b, _ := json.Marshal(v); return b }
func encode(b []byte) string          { return base64.RawStdEncoding.EncodeToString(b) }
func decode(s string) ([]byte, error) { return base64.RawStdEncoding.DecodeString(s) }
func hash(s string) string            { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func ClaimMessage(c Claim) []byte {
	return []byte("JD-VANTAGE-ENROLL-1\n" + c.ServerKey + "\n" + c.ID + "\n" + hash(c.Token) + "\n" + c.PublicKey)
}
func RequestMessage(method, path string, body []byte, s Signature) []byte {
	sum := sha256.Sum256(body)
	return []byte(fmt.Sprintf("JD-VANTAGE-REQUEST-1\n%s\n%s\n%s\n%x\n%s\n%d\n%d", s.ServerKey, method, path, sum, s.ID, s.Sequence, s.Timestamp))
}
func SignRequest(key ed25519.PrivateKey, method, path string, body []byte, s Signature) Signature {
	s.Value = encode(ed25519.Sign(key, RequestMessage(method, path, body, s)))
	return s
}
func SignClaim(key ed25519.PrivateKey, c Claim) Claim {
	c.Proof = encode(ed25519.Sign(key, ClaimMessage(c)))
	return c
}
func publicKey(key ed25519.PrivateKey) string { return encode(key.Public().(ed25519.PublicKey)) }
func VerifyJob(s SignedJob, key string) error {
	pub, e := decode(key)
	sig, er := decode(s.Signature)
	if e != nil || er != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, Marshal(s.Job), sig) {
		return fmt.Errorf("job signature is invalid")
	}
	return nil
}
