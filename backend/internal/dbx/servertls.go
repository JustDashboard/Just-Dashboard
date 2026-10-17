package dbx

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// A database the dashboard started speaks TLS with a certificate signed by the
// dashboard's own authority, and the dashboard's saved connection to it
// verifies that certificate rather than skipping the check. The authority's
// certificate is a file on this machine, and the connection string names it
// the way each engine's own client does:
//
//   - PostgreSQL: sslmode=verify-full and sslrootcert=<file>
//   - MongoDB: tls=true and tlsCAFile=<file>
//   - Redis: the rediss scheme and tlsCAFile=<file>
//   - MySQL and MariaDB: tlsCAFile=<file>, which the Go driver has no word
//     for, so NormaliseDSN turns it into a registered TLS configuration
//     before the driver reads the string.
//
// The two that are not an engine's own spelling (Redis and MySQL) are read only
// by this package, so a string carrying one must never reach another program:
// go-sql-driver would send tlsCAFile to the server as a system variable and
// fail. An application, which is in another container and has no such file,
// is given ForApplication's string instead.

// serverCAParam is the parameter that names the authority's certificate in the
// two connection strings whose drivers have none of their own.
const serverCAParam = "tlsCAFile"

// WithServerTLS returns the connection string with TLS switched on and checked
// against the certificate in caFile.
func WithServerTLS(driver Driver, dsn, caFile string) (string, error) {
	if strings.TrimSpace(caFile) == "" {
		return "", errors.New("the authority's certificate file is not known")
	}
	switch driver {
	case DriverPostgres:
		return editQuery(dsn, "postgres", "postgresql", func(u *url.URL, q url.Values) {
			q.Set("sslmode", "verify-full")
			q.Set("sslrootcert", caFile)
		})
	case DriverMongo:
		return editQuery(dsn, "mongodb", "", func(u *url.URL, q url.Values) {
			q.Set("tls", "true")
			q.Set(serverCAParam, caFile)
		})
	case DriverRedis:
		return editQuery(dsn, "redis", "rediss", func(u *url.URL, q url.Values) {
			u.Scheme = "rediss"
			q.Set(serverCAParam, caFile)
		})
	case DriverMySQL:
		return editMySQL(dsn, func(cfg *mysql.Config) {
			if cfg.Params == nil {
				cfg.Params = map[string]string{}
			}
			cfg.Params[serverCAParam] = caFile
		})
	}
	return "", fmt.Errorf("%s has no server TLS", driver)
}

// WithoutServerTLS undoes WithServerTLS: the connection string as it was before
// TLS was switched on.
func WithoutServerTLS(driver Driver, dsn string) (string, error) {
	switch driver {
	case DriverPostgres:
		return editQuery(dsn, "postgres", "postgresql", func(u *url.URL, q url.Values) {
			q.Set("sslmode", "disable")
			q.Del("sslrootcert")
		})
	case DriverMongo:
		return editQuery(dsn, "mongodb", "", func(u *url.URL, q url.Values) {
			q.Del("tls")
			q.Del(serverCAParam)
		})
	case DriverRedis:
		return editQuery(dsn, "redis", "rediss", func(u *url.URL, q url.Values) {
			u.Scheme = "redis"
			q.Del(serverCAParam)
		})
	case DriverMySQL:
		return editMySQL(dsn, func(cfg *mysql.Config) { delete(cfg.Params, serverCAParam) })
	}
	return "", fmt.Errorf("%s has no server TLS", driver)
}

// ServerTLSFile is the authority file a connection string verifies against, or
// empty when the connection is not set up for server TLS.
func ServerTLSFile(driver Driver, dsn string) string {
	switch driver {
	case DriverPostgres:
		if u, err := url.Parse(dsn); err == nil && u.Query().Get("sslmode") == "verify-full" {
			return u.Query().Get("sslrootcert")
		}
	case DriverMongo, DriverRedis:
		if u, err := url.Parse(dsn); err == nil {
			return u.Query().Get(serverCAParam)
		}
	case DriverMySQL:
		if !strings.Contains(dsn, "://") {
			if cfg, err := mysql.ParseDSN(dsn); err == nil {
				return cfg.Params[serverCAParam]
			}
		}
	}
	return ""
}

// ForApplication is the connection string for a program in another container.
// The authority's file is this machine's path and the parameters that name it
// mean nothing to the program, so they are taken out, and what is left says what
// that program has to do:
//
//   - TLS is allowed but not required: the string as it was before TLS. A
//     program with no copy of the authority is not made to use TLS it cannot
//     check, and the server still accepts it.
//   - TLS is required: a string that uses TLS and does not verify the
//     certificate where the driver has a word for that (PostgreSQL
//     sslmode=require, MongoDB tlsInsecure, MySQL tls=skip-verify). Redis has no
//     such word in a URL, so its string is rediss:// alone and the program
//     needs the authority's certificate.
//
// A string that is not set up for server TLS is returned as it is.
func ForApplication(driver Driver, dsn string, required bool) string {
	if ServerTLSFile(driver, dsn) == "" {
		return dsn
	}
	if !required && driver != DriverRedis {
		if out, err := WithoutServerTLS(driver, dsn); err == nil {
			return out
		}
		return dsn
	}
	switch driver {
	case DriverPostgres:
		if out, err := editQuery(dsn, "postgres", "postgresql", func(u *url.URL, q url.Values) {
			q.Set("sslmode", "require")
			q.Del("sslrootcert")
		}); err == nil {
			return out
		}
	case DriverMongo:
		if out, err := editQuery(dsn, "mongodb", "", func(u *url.URL, q url.Values) {
			q.Set("tls", "true")
			q.Set("tlsInsecure", "true")
			q.Del(serverCAParam)
		}); err == nil {
			return out
		}
	case DriverRedis:
		if out, err := editQuery(dsn, "redis", "rediss", func(u *url.URL, q url.Values) { q.Del(serverCAParam) }); err == nil {
			return out
		}
	case DriverMySQL:
		if out, err := editMySQL(dsn, func(cfg *mysql.Config) {
			delete(cfg.Params, serverCAParam)
			cfg.TLSConfig = "skip-verify"
		}); err == nil {
			return out
		}
	}
	return dsn
}

// ServerTLSRequired reports a connection string that must be spoken over TLS.
func ServerTLSRequired(driver Driver, dsn string) bool {
	return ServerTLSFile(driver, dsn) != ""
}

func editQuery(dsn, scheme, alt string, edit func(*url.URL, url.Values)) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return "", errors.New("this connection string cannot carry TLS settings")
	}
	if scheme != "" && u.Scheme != scheme && u.Scheme != alt {
		return "", fmt.Errorf("a %s connection string was expected", scheme)
	}
	q := u.Query()
	edit(u, q)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func editMySQL(dsn string, edit func(*mysql.Config)) (string, error) {
	if strings.Contains(dsn, "://") {
		return "", errors.New("this connection string cannot carry TLS settings")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", errors.New("this connection string cannot carry TLS settings")
	}
	edit(cfg)
	return cfg.FormatDSN(), nil
}

// mysqlServerCA is NormaliseDSN for MySQL: where the connection string names an
// authority file, the file becomes a TLS configuration registered with the
// driver and the string names that. A string that does not, or whose file
// cannot be read, goes through untouched, and the driver's own error says what
// is wrong.
func mysqlServerCA(dsn string) string {
	if !strings.Contains(dsn, serverCAParam+"=") {
		return dsn
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return dsn
	}
	file, ok := cfg.Params[serverCAParam]
	if !ok {
		return dsn
	}
	roots, err := serverRoots(file)
	if err != nil {
		return dsn
	}
	sum := sha256.Sum256([]byte(file))
	name := "jd-ca-" + hex.EncodeToString(sum[:6])
	if err := mysql.RegisterTLSConfig(name, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}); err != nil {
		return dsn
	}
	delete(cfg.Params, serverCAParam)
	cfg.TLSConfig = name
	return cfg.FormatDSN()
}

// serverRoots reads an authority file into a pool of roots.
func serverRoots(file string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("the authority file holds no certificate")
	}
	return pool, nil
}
