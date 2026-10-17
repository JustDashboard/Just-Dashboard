package dbx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// ServedCertificate dials a server the way its client would, as far as the TLS
// handshake, and returns the certificate it presents, checked against the
// authority's file for the host in addr. It is how a certificate that was just
// replaced is told from one the server is still holding: a server asked to
// reload its files may say it did and carry on with the old ones.
func ServedCertificate(ctx context.Context, driver Driver, addr, caFile string) (*x509.Certificate, error) {
	roots, err := serverRoots(caFile)
	if err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	switch driver {
	case DriverPostgres:
		if err := postgresStartTLS(conn); err != nil {
			return nil, err
		}
	case DriverMySQL:
		if err := mysqlStartTLS(conn); err != nil {
			return nil, err
		}
	}
	secured := tls.Client(conn, &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12})
	if err := secured.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	return secured.ConnectionState().PeerCertificates[0], nil
}

// postgresStartTLS sends the SSLRequest message and reads the one byte that
// says whether the server will switch.
func postgresStartTLS(conn net.Conn) error {
	if _, err := conn.Write([]byte{0, 0, 0, 8, 0x04, 0xd2, 0x16, 0x2f}); err != nil {
		return err
	}
	answer := make([]byte, 1)
	if _, err := io.ReadFull(conn, answer); err != nil {
		return err
	}
	if answer[0] != 'S' {
		return fmt.Errorf("the server does not offer TLS")
	}
	return nil
}

// mysqlStartTLS reads the server's greeting and answers with the short
// handshake that asks to switch to TLS, which MySQL and MariaDB both take.
func mysqlStartTLS(conn net.Conn) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	size := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	if _, err := io.CopyN(io.Discard, conn, int64(size)); err != nil {
		return err
	}
	const (
		protocol41       = 0x00000200
		clientSSL        = 0x00000800
		secureConnection = 0x00008000
		pluginAuth       = 0x00080000
	)
	request := make([]byte, 4+32)
	request[0], request[3] = 32, 1
	binary.LittleEndian.PutUint32(request[4:], protocol41|clientSSL|secureConnection|pluginAuth)
	binary.LittleEndian.PutUint32(request[8:], 1<<24)
	request[12] = 45 // utf8mb4_general_ci
	_, err := conn.Write(request)
	return err
}
