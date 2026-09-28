package proxysvc

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Identification is what a listening TCP socket says when asked: the banner a
// server that speaks first sends, else whether it speaks TLS and what its
// certificate and ALPN are, and what an HTTP request gets back. A port's
// number says what it usually is; this says what it is.
type Identification struct {
	// Endpoint is the address dialled: a wildcard socket is asked on the
	// loopback address of its family.
	Endpoint string `json:"endpoint"`
	// Connected is false where the connection itself failed, and Error then
	// says why.
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
	// Banner is the first line a server that speaks first sent (SSH, SMTP,
	// FTP), with anything unprintable shown as a dot. A socket that sent one
	// is not asked for TLS or HTTP: it has said what it is.
	Banner string              `json:"banner,omitempty"`
	TLS    *IdentifiedTLS      `json:"tls,omitempty"`
	HTTP   *IdentifiedHTTP     `json:"http,omitempty"`
	Steps  []IdentifyStepError `json:"steps,omitempty"`
}

// IdentifiedTLS is the handshake a socket completed, or the alert it refused
// one with, which still proves it speaks TLS.
type IdentifiedTLS struct {
	// Alert is the server's refusal, as Go words it: a server that wants a
	// name it was not sent refuses rather than answering with a default.
	Alert    string     `json:"alert,omitempty"`
	Version  string     `json:"version,omitempty"`
	ALPN     string     `json:"alpn,omitempty"`
	Subject  string     `json:"subject,omitempty"`
	Names    []string   `json:"names,omitempty"`
	Issuer   string     `json:"issuer,omitempty"`
	NotAfter *time.Time `json:"notAfter,omitempty"`
	// SelfSigned is a leaf that issued itself.
	SelfSigned bool `json:"selfSigned,omitempty"`
}

// IdentifiedHTTP is the answer to HEAD /.
type IdentifiedHTTP struct {
	Status int    `json:"status"`
	Reason string `json:"reason,omitempty"`
	Server string `json:"server,omitempty"`
	// Location is where a redirect points, so "301 to https://…" reads as
	// the redirect it is.
	Location string `json:"location,omitempty"`
}

// IdentifyStepError is a step that did not answer, so a blank TLS or HTTP
// reads as "asked and got nothing" rather than "not asked".
type IdentifyStepError struct {
	Step  string `json:"step"`
	Error string `json:"error"`
}

// Each step's own bound, all inside the caller's. A server that speaks first
// does so on accepting; one second is long for that, short for a person.
const (
	bannerWait   = 1500 * time.Millisecond
	handshakeCap = 2 * time.Second
	httpCap      = 1500 * time.Millisecond
	bannerMax    = 256
)

// ListeningAt reports whether a socket accepts on exactly this protocol,
// address and port, read from the kernel's tables as the listing reads them.
// Identify is only ever pointed at one of these: a probe whose target the
// caller chooses freely is a port scanner.
func ListeningAt(protocol, address string, port uint32) (bool, error) {
	sockets, err := readPortSockets(procRoot())
	if err != nil {
		return false, err
	}
	for _, s := range sockets {
		if s.proto == protocol && s.address == address && s.port == port && s.listening() {
			return true, nil
		}
	}
	return false, nil
}

// IdentifyTarget is the address to dial for a socket bound to address: its
// own, or its family's loopback for a wildcard.
func IdentifyTarget(address string, port uint32) string {
	host := address
	switch address {
	case "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10))
}

// Identify asks the TCP socket at target what it is. serverName, where given,
// is sent as SNI and as the Host header. The certificate is read, never
// trusted: the point is to see what the socket presents.
func Identify(ctx context.Context, target, serverName string) *Identification {
	out := &Identification{Endpoint: target}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Connected = true
	banner := readBanner(ctx, conn)
	conn.Close()
	if banner != "" {
		out.Banner = banner
		return out
	}

	host := serverName
	if host == "" {
		host = target
	}
	state, err := identifyHandshake(ctx, &dialer, target, serverName, []string{"h2", "http/1.1"})
	var alert tls.AlertError
	switch {
	case err == nil:
		out.TLS = describeLeaf(state)
		out.HTTP, err = headOverTLS(ctx, &dialer, target, serverName, host)
		if err != nil {
			out.Steps = append(out.Steps, IdentifyStepError{Step: "http", Error: err.Error()})
		}
		return out
	case errors.As(err, &alert):
		out.TLS = &IdentifiedTLS{Alert: err.Error()}
		return out
	default:
		out.Steps = append(out.Steps, IdentifyStepError{Step: "tls", Error: err.Error()})
	}

	plain, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		out.Steps = append(out.Steps, IdentifyStepError{Step: "http", Error: err.Error()})
		return out
	}
	defer plain.Close()
	out.HTTP, err = head(ctx, plain, host)
	if err != nil {
		out.Steps = append(out.Steps, IdentifyStepError{Step: "http", Error: err.Error()})
	}
	return out
}

// deadline is now plus step, or the caller's deadline where that is sooner.
func deadline(ctx context.Context, step time.Duration) time.Time {
	at := time.Now().Add(step)
	if d, ok := ctx.Deadline(); ok && d.Before(at) {
		return d
	}
	return at
}

func readBanner(ctx context.Context, conn net.Conn) string {
	_ = conn.SetReadDeadline(deadline(ctx, bannerWait))
	buf := make([]byte, bannerMax)
	n, _ := conn.Read(buf)
	line, _, _ := strings.Cut(string(buf[:n]), "\n")
	return printableBanner(strings.TrimRight(line, "\r"))
}

// printableBanner keeps a banner to what a page can show: a binary protocol's
// greeting (MySQL's) is mostly unprintable, and its version string is not.
func printableBanner(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		} else {
			b.WriteByte('.')
		}
	}
	return strings.TrimSpace(b.String())
}

func identifyHandshake(ctx context.Context, dialer *net.Dialer, target, serverName string, alpn []string) (tls.ConnectionState, error) {
	conn, err := tlsDial(ctx, dialer, target, serverName, alpn)
	if err != nil {
		return tls.ConnectionState{}, err
	}
	defer conn.Close()
	return conn.ConnectionState(), nil
}

func tlsDial(ctx context.Context, dialer *net.Dialer, target, serverName string, alpn []string) (*tls.Conn, error) {
	raw, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, err
	}
	_ = raw.SetDeadline(deadline(ctx, handshakeCap))
	conn := tls.Client(raw, &tls.Config{
		ServerName: serverName,
		NextProtos: alpn,
		// The socket's own certificate is what is being read; verifying it
		// would hide exactly the self-signed or mismatched one worth seeing.
		InsecureSkipVerify: true, //nolint:gosec
	})
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return conn, nil
}

func describeLeaf(state tls.ConnectionState) *IdentifiedTLS {
	out := &IdentifiedTLS{Version: tls.VersionName(state.Version), ALPN: state.NegotiatedProtocol}
	if len(state.PeerCertificates) == 0 {
		return out
	}
	leaf := state.PeerCertificates[0]
	out.Subject = leaf.Subject.CommonName
	out.Names = append(out.Names, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		out.Names = append(out.Names, ip.String())
	}
	out.Issuer = leaf.Issuer.CommonName
	if out.Issuer == "" && len(leaf.Issuer.Organization) > 0 {
		out.Issuer = leaf.Issuer.Organization[0]
	}
	notAfter := leaf.NotAfter.UTC()
	out.NotAfter = &notAfter
	out.SelfSigned = issuedItself(leaf)
	return out
}

func issuedItself(leaf *x509.Certificate) bool {
	return leaf.CheckSignatureFrom(leaf) == nil
}

// headOverTLS asks over a second handshake offering only HTTP/1.1: a server
// that chose h2 on the first would not read an HTTP/1.1 request.
func headOverTLS(ctx context.Context, dialer *net.Dialer, target, serverName, host string) (*IdentifiedHTTP, error) {
	conn, err := tlsDial(ctx, dialer, target, serverName, []string{"http/1.1"})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return head(ctx, conn, host)
}

func head(ctx context.Context, conn net.Conn, host string) (*IdentifiedHTTP, error) {
	_ = conn.SetDeadline(deadline(ctx, httpCap))
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "/", nil)
	if err != nil {
		return nil, err
	}
	req.Host = host
	req.Close = true
	req.Header.Set("User-Agent", "Just-Dashboard-identify")
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	return &IdentifiedHTTP{
		Status:   resp.StatusCode,
		Reason:   strings.TrimSpace(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode))),
		Server:   printableBanner(resp.Header.Get("Server")),
		Location: printableBanner(resp.Header.Get("Location")),
	}, nil
}
