// Package accountlink links this dashboard to a just-dashboard.com account.
//
// The dashboard is never reachable from the internet, so everything here is
// outbound: it asks the site for a short code, a person approves that code
// while signed in on the site, and from then on the dashboard sends a heartbeat
// and holds a long poll open for commands. Every request is signed with the
// install's own Ed25519 key, and every command must verify against the site key
// pinned at link time, fit the scope chosen on this machine and not have been
// seen before, or it does not run.
//
// The wire contract is docs/server-link-protocol.md in the site's repository,
// https://github.com/JustDashboard/frontend/blob/main/docs/server-link-protocol.md.
// This file holds the parts of it two implementations have to agree on byte for
// byte: the signed strings, the canonical JSON of a command's arguments and the
// key fingerprint.
package accountlink

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// ProtocolVersion is the only version this build speaks.
	ProtocolVersion = 1

	basePath = "/api/link/v1"

	headerProtocol  = "X-JD-Link-Protocol"
	headerServer    = "X-JD-Server"
	headerTime      = "X-JD-Time"
	headerSequence  = "X-JD-Sequence"
	headerSignature = "X-JD-Signature"

	maxRequestBody  = 64 << 10
	maxResponseBody = 256 << 10
)

// Scope is how much of this machine a linked account may see and do. It is
// chosen here, stored here and enforced here; the site only draws the controls
// it is told are allowed.
type Scope string

const (
	ScopeObserve  Scope = "observe"
	ScopeOperate  Scope = "operate"
	ScopeMaintain Scope = "maintain"
)

// ParseScope accepts exactly the three names.
func ParseScope(s string) (Scope, bool) {
	switch sc := Scope(s); sc {
	case ScopeObserve, ScopeOperate, ScopeMaintain:
		return sc, true
	}
	return "", false
}

func (s Scope) rank() int {
	switch s {
	case ScopeObserve:
		return 1
	case ScopeOperate:
		return 2
	case ScopeMaintain:
		return 3
	}
	return 0
}

// Includes reports whether holding s allows what need allows. Scopes nest:
// operate includes observe, and maintain includes both.
func (s Scope) Includes(need Scope) bool { return s.rank() > 0 && s.rank() >= need.rank() }

// The command kinds a v1 site may send. Anything else is refused.
const (
	KindSnapshotNow     = "snapshot.now"
	KindContainerStart  = "container.start"
	KindContainerStop   = "container.stop"
	KindContainerReboot = "container.restart"
	KindStackRestart    = "stack.restart"
	KindBackupRun       = "backup.run"
	KindUpdateInstall   = "dashboard.update.install"
	KindUpdatePolicy    = "dashboard.update.policy"
	KindRestart         = "dashboard.restart"
)

var kindScope = map[string]Scope{
	KindSnapshotNow:     ScopeObserve,
	KindContainerStart:  ScopeOperate,
	KindContainerStop:   ScopeOperate,
	KindContainerReboot: ScopeOperate,
	KindStackRestart:    ScopeOperate,
	KindBackupRun:       ScopeOperate,
	KindUpdateInstall:   ScopeMaintain,
	KindUpdatePolicy:    ScopeMaintain,
	KindRestart:         ScopeMaintain,
}

// RequiredScope is the scope a kind needs, and whether the kind exists.
func RequiredScope(kind string) (Scope, bool) {
	s, ok := kindScope[kind]
	return s, ok
}

// Fingerprint is the first 16 hex characters of SHA-256 over the raw public
// key, in four groups, so a person can compare what the approval page shows
// with what this dashboard shows.
func Fingerprint(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	h := hex.EncodeToString(sum[:8])
	return h[0:4] + ":" + h[4:8] + ":" + h[8:12] + ":" + h[12:16]
}

// requestMessage is the string a request signature covers: five lines, no
// trailing newline. The path is exactly as sent, query included.
func requestMessage(method, pathWithQuery string, body []byte, unixTime, sequence int64) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		method, pathWithQuery, hex.EncodeToString(sum[:]),
		strconv.FormatInt(unixTime, 10), strconv.FormatInt(sequence, 10),
	}, "\n")
}

func signRequest(key ed25519.PrivateKey, method, pathWithQuery string, body []byte, unixTime, sequence int64) string {
	msg := requestMessage(method, pathWithQuery, body, unixTime, sequence)
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(msg)))
}

// commandMessage is the string the site's signature on a command covers. It
// names this server, so a command signed for another one fails here.
func commandMessage(serverID, commandID, kind string, canonicalArgs []byte, expiresAt int64) string {
	sum := sha256.Sum256(canonicalArgs)
	return strings.Join([]string{
		serverID, commandID, kind, hex.EncodeToString(sum[:]), strconv.FormatInt(expiresAt, 10),
	}, "\n")
}

// maxSafeInt is the largest integer both Go and JavaScript hold exactly, which
// is what keeps the two sides' canonical JSON identical.
const maxSafeInt = 1<<53 - 1

var integerLiteral = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// decodeArgs reads a command's arguments into the small value set the protocol
// allows: objects, strings, integers and booleans. Anything else — an array, a
// null, a fraction, a number JavaScript could not hold — is an error, because
// no implementation could be trusted to canonicalise it the same way.
func decodeArgs(raw json.RawMessage) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("arguments are missing")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, fmt.Errorf("arguments are not JSON: %w", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("arguments have trailing content")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("arguments must be an object")
	}
	if err := checkArgValue(obj, 0); err != nil {
		return nil, err
	}
	return obj, nil
}

func checkArgValue(v any, depth int) error {
	if depth > 4 {
		return errors.New("arguments are nested too deeply")
	}
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if !utf8.ValidString(k) {
				return errors.New("an argument name is not valid UTF-8")
			}
			if err := checkArgValue(child, depth+1); err != nil {
				return err
			}
		}
	case string:
		if !utf8.ValidString(t) {
			return errors.New("an argument value is not valid UTF-8")
		}
	case bool:
	case json.Number:
		if !integerLiteral.MatchString(t.String()) {
			return fmt.Errorf("%s is not a plain integer", t)
		}
		n, err := t.Int64()
		if err != nil || n > maxSafeInt || n < -maxSafeInt {
			return fmt.Errorf("%s is outside the range the protocol allows", t)
		}
	default:
		return fmt.Errorf("%T is not an allowed argument value", v)
	}
	return nil
}

// canonicalJSON renders a value decodeArgs accepted: keys sorted by code point,
// no whitespace, strings escaped for `"`, `\` and control characters only.
func canonicalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := writeCanonical(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeCanonical(b *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		// UTF-8 orders the same way code points do, so a byte sort is the
		// sort the protocol asks for.
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeCanonicalString(b, k)
			b.WriteByte(':')
			if err := writeCanonical(b, t[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case string:
		writeCanonicalString(b, t)
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return err
		}
		b.WriteString(strconv.FormatInt(n, 10))
	case int:
		b.WriteString(strconv.Itoa(t))
	case int64:
		b.WriteString(strconv.FormatInt(t, 10))
	case float64:
		if t != math.Trunc(t) || math.Abs(t) > maxSafeInt {
			return fmt.Errorf("%v is not a plain integer", t)
		}
		b.WriteString(strconv.FormatInt(int64(t), 10))
	default:
		return fmt.Errorf("%T cannot be canonicalised", v)
	}
	return nil
}

func writeCanonicalString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

// decodeStrict reads a response body the way a v1 site promises to write it:
// no unknown fields and nothing after the value.
func decodeStrict(body []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("the response has content after its JSON value")
	}
	return nil
}
