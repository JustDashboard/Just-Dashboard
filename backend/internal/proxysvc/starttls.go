package proxysvc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
)

// STARTTLS: the plain-text dialogue some services hold before their handshake.
//
// A mail, FTP or PostgreSQL server on its usual port speaks its own protocol
// first and upgrades the connection only when asked, so a handshake sent as
// the first byte reads to it as garbage and the scan used to report the port
// as not speaking TLS at all. The dialogue here is the least that reaches the
// upgrade. It is strict — any answer the protocol does not define stops it,
// and so does a byte sent after the server agreed, which a client that went on
// would read as if it had come over TLS (CVE-2011-0411, CVE-2021-23214) — and
// bounded: in time by the handshake's own deadline, in size by
// maxDialogueLine and maxDialogueLines, since the other end may be anything.

// startTLSNames are the dialogues a scan can hold, by the ?proto= value that
// asks for one, with the name a report shows.
var startTLSNames = map[string]string{
	"smtp": "SMTP", "imap": "IMAP", "pop3": "POP3", "ftp": "FTP", "postgres": "PostgreSQL",
}

// startTLSPorts are the ports whose service upgrades with STARTTLS, which
// Auto holds that dialogue on. 587 is submission, which the RFC wants
// upgraded; 465 and 993 speak TLS from the first byte (implicitTLSServices).
var startTLSPorts = map[int]string{
	21: "ftp", 25: "smtp", 110: "pop3", 143: "imap", 587: "smtp", 5432: "postgres",
}

// ParseStartTLS reads ?proto=: "" or "auto" picks the dialogue by port, "tls"
// speaks TLS from the first byte whatever the port, and anything else names a
// dialogue. It returns the dialogue to hold, "" for none.
func ParseStartTLS(raw string, port int) (string, error) {
	switch raw {
	case "", "auto":
		return startTLSPorts[port], nil
	case "tls":
		return "", nil
	}
	if _, known := startTLSNames[raw]; known {
		return raw, nil
	}
	return "", fmt.Errorf("proto takes auto, tls, smtp, imap, pop3, ftp or postgres, not %q", raw)
}

const (
	// maxDialogueLine is longer than any line these protocols define (SMTP's
	// is 512 octets, RFC 5321 4.5.3.1.5); a longer one is not the protocol.
	maxDialogueLine = 4096
	// maxDialogueLines bounds a multi-line reply: an EHLO answer lists a
	// dozen extensions, and a server sending hundreds is not answering it.
	maxDialogueLines = 100
)

// startTLSError is a STARTTLS dialogue that did not reach the handshake.
// Reason is "refused" (the server spoke the protocol and would not upgrade),
// "unexpected" (it did not speak the protocol, or spoke out of turn), or, with
// err set, the connection failing under it.
type startTLSError struct {
	protocol string
	reason   string
	reply    string
	err      error
}

func (e *startTLSError) Error() string {
	name := startTLSNames[e.protocol]
	switch {
	case e.err != nil:
		return name + " STARTTLS: " + e.err.Error()
	case e.reply != "":
		return fmt.Sprintf("%s STARTTLS %s: %q", name, e.reason, e.reply)
	}
	return name + " STARTTLS " + e.reason
}

func (e *startTLSError) Unwrap() error { return e.err }

// failureReason is the ScanFailure reason for the dialogue's end.
func (e *startTLSError) failureReason() string {
	switch {
	case e.err == nil:
		return "starttls-" + e.reason
	case isTimeout(e.err):
		return "starttls-timeout"
	case errors.Is(e.err, io.EOF), errors.Is(e.err, syscall.ECONNRESET):
		return "starttls-closed"
	}
	return "starttls-error"
}

// dialogue is one STARTTLS exchange on conn.
type dialogue struct {
	protocol string
	conn     net.Conn
	in       *bufio.Reader
}

// startTLS holds protocol's dialogue on conn up to the point where the server
// waits for a ClientHello. The caller bounds it in time by conn's deadline.
func startTLS(conn net.Conn, protocol string) error {
	d := &dialogue{protocol: protocol, conn: conn, in: bufio.NewReaderSize(conn, maxDialogueLine)}
	var err error
	switch protocol {
	case "smtp":
		err = d.smtp()
	case "imap":
		err = d.imap()
	case "pop3":
		err = d.pop3()
	case "ftp":
		err = d.ftp()
	case "postgres":
		err = d.postgres()
	default:
		return fmt.Errorf("no STARTTLS dialogue for %q", protocol)
	}
	if err != nil {
		return err
	}
	if d.in.Buffered() > 0 {
		return d.fail("unexpected", "sent more after agreeing: "+said(string(peekAll(d.in))))
	}
	return nil
}

func peekAll(r *bufio.Reader) []byte {
	b, _ := r.Peek(min(r.Buffered(), 80))
	return b
}

func (d *dialogue) fail(reason, reply string) error {
	return &startTLSError{protocol: d.protocol, reason: reason, reply: reply}
}

func (d *dialogue) send(line string) error {
	if _, err := io.WriteString(d.conn, line+"\r\n"); err != nil {
		return &startTLSError{protocol: d.protocol, err: err}
	}
	return nil
}

// line reads one CRLF- or LF-ended line without its ending.
func (d *dialogue) line() (string, error) {
	raw, err := d.in.ReadSlice('\n')
	switch {
	case errors.Is(err, bufio.ErrBufferFull):
		return "", d.fail("unexpected", "a line longer than "+fmt.Sprint(maxDialogueLine)+" bytes")
	case err != nil:
		return "", &startTLSError{protocol: d.protocol, err: err}
	}
	return string(bytes.TrimRight(raw, "\r\n")), nil
}

// reply reads an SMTP or FTP reply: a three-digit code, and for a multi-line
// reply every line up to the one with a space after the code. The lines are
// returned without their codes.
func (d *dialogue) reply() (code string, lines []string, err error) {
	first, err := d.line()
	if err != nil {
		return "", nil, err
	}
	if !replyCode(first) {
		return "", nil, d.fail("unexpected", said(first))
	}
	code = first[:3]
	lines = []string{strings.TrimSpace(first[3:])}
	for len(first) > 3 && first[3] == '-' {
		if len(lines) == maxDialogueLines {
			return "", nil, d.fail("unexpected", "a reply of more than "+fmt.Sprint(maxDialogueLines)+" lines")
		}
		next, err := d.line()
		if err != nil {
			return "", nil, err
		}
		// SMTP repeats the code on every line; FTP's middle lines may be
		// anything, and its last starts with the code and a space.
		if strings.HasPrefix(next, code+" ") || next == code {
			lines = append(lines, strings.TrimSpace(next[3:]))
			break
		}
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(next, code+"-")))
	}
	return code, lines, nil
}

func replyCode(line string) bool {
	if len(line) < 3 || (len(line) > 3 && line[3] != ' ' && line[3] != '-') {
		return false
	}
	for _, c := range line[:3] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// expect reads a reply and stops the dialogue unless it has code want: a
// 4xx or 5xx is the server refusing, anything else is out of turn.
func (d *dialogue) expect(want string) ([]string, error) {
	code, lines, err := d.reply()
	if err != nil {
		return nil, err
	}
	if code == want {
		return lines, nil
	}
	reason := "unexpected"
	if code[0] == '4' || code[0] == '5' {
		reason = "refused"
	}
	return nil, d.fail(reason, said(code+" "+strings.Join(lines, " ")))
}

// smtp is RFC 3207: the greeting, EHLO, STARTTLS in the extensions, STARTTLS.
// EHLO names localhost, as Go's net/smtp does: the scan is not a mail server
// and should not claim a name, and every server takes that one.
func (d *dialogue) smtp() error {
	if _, err := d.expect("220"); err != nil {
		return err
	}
	if err := d.send("EHLO localhost"); err != nil {
		return err
	}
	extensions, err := d.expect("250")
	if err != nil {
		return err
	}
	offered := false
	for _, ext := range extensions[1:] {
		if words := strings.Fields(ext); len(words) > 0 && strings.EqualFold(words[0], "STARTTLS") {
			offered = true
		}
	}
	if !offered {
		return d.fail("refused", "EHLO listed no STARTTLS")
	}
	if err := d.send("STARTTLS"); err != nil {
		return err
	}
	_, err = d.expect("220")
	return err
}

// ftp is RFC 4217: the greeting, then AUTH TLS answered with 234.
func (d *dialogue) ftp() error {
	if _, err := d.expect("220"); err != nil {
		return err
	}
	if err := d.send("AUTH TLS"); err != nil {
		return err
	}
	_, err := d.expect("234")
	return err
}

// imap is RFC 9051 6.2.1: an OK greeting, then a tagged STARTTLS answered OK.
// Untagged lines before the tagged answer are allowed and skipped.
func (d *dialogue) imap() error {
	greeting, err := d.line()
	if err != nil {
		return err
	}
	switch {
	case hasPrefixFold(greeting, "* OK"):
	case hasPrefixFold(greeting, "* BYE"), hasPrefixFold(greeting, "* PREAUTH"):
		// PREAUTH is a session already authenticated, in which STARTTLS
		// is not allowed.
		return d.fail("refused", said(greeting))
	default:
		return d.fail("unexpected", said(greeting))
	}
	if err := d.send("a1 STARTTLS"); err != nil {
		return err
	}
	for range maxDialogueLines {
		answer, err := d.line()
		if err != nil {
			return err
		}
		switch {
		case strings.HasPrefix(answer, "* "):
			continue
		case hasPrefixFold(answer, "a1 OK"):
			return nil
		case hasPrefixFold(answer, "a1 NO"), hasPrefixFold(answer, "a1 BAD"):
			return d.fail("refused", said(answer))
		}
		return d.fail("unexpected", said(answer))
	}
	return d.fail("unexpected", "no answer to STARTTLS in "+fmt.Sprint(maxDialogueLines)+" lines")
}

// pop3 is RFC 2595 4: a +OK greeting, then STLS answered +OK.
func (d *dialogue) pop3() error {
	expectOK := func() error {
		answer, err := d.line()
		switch {
		case err != nil:
			return err
		case strings.HasPrefix(answer, "+OK"):
			return nil
		case strings.HasPrefix(answer, "-ERR"):
			return d.fail("refused", said(answer))
		}
		return d.fail("unexpected", said(answer))
	}
	if err := expectOK(); err != nil {
		return err
	}
	if err := d.send("STLS"); err != nil {
		return err
	}
	return expectOK()
}

// postgresSSLRequest is the SSLRequest message: its length, 8, and the code
// 80877103 (PostgreSQL protocol, 54.2.10).
var postgresSSLRequest = []byte{0, 0, 0, 8, 0x04, 0xd2, 0x16, 0x2f}

// postgres sends SSLRequest, which the server answers with one byte: S to
// go on with a handshake, N when it has ssl off. A server too old to know
// the message answers with an error, E.
func (d *dialogue) postgres() error {
	if _, err := d.conn.Write(postgresSSLRequest); err != nil {
		return &startTLSError{protocol: d.protocol, err: err}
	}
	answer, err := d.in.ReadByte()
	if err != nil {
		return &startTLSError{protocol: d.protocol, err: err}
	}
	switch answer {
	case 'S':
		return nil
	case 'N':
		return d.fail("refused", "N: the server has ssl off")
	}
	return d.fail("unexpected", said(string([]byte{answer})))
}

// said is what the server said, as a report can show it: printable, and cut
// short, since a line may be up to maxDialogueLine long.
func said(s string) string {
	shown := printable([]byte(s))
	if len(shown) > 160 {
		shown = shown[:160] + "…"
	}
	return shown
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
