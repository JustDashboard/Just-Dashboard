package dbx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbtls"
	"github.com/go-sql-driver/mysql"
)

const testCAFile = "/var/lib/just-dashboard/tls/ca.pem"

func TestServerTLSRoundTripsEveryEngine(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		plain  string
		secure []string
	}{
		{DriverPostgres, "postgres://jd:pw@127.0.0.1:5432/app?sslmode=disable",
			[]string{"sslmode=verify-full", "sslrootcert=" + "%2Fvar%2Flib%2Fjust-dashboard%2Ftls%2Fca.pem"}},
		{DriverMongo, "mongodb://jd:pw@127.0.0.1:27017/app?authSource=admin",
			[]string{"tls=true", "tlsCAFile=%2Fvar%2Flib%2Fjust-dashboard%2Ftls%2Fca.pem", "authSource=admin"}},
		{DriverRedis, "redis://:pw@127.0.0.1:6379/0",
			[]string{"rediss://", "tlsCAFile=%2Fvar%2Flib%2Fjust-dashboard%2Ftls%2Fca.pem"}},
		{DriverMySQL, "jd:pw@tcp(127.0.0.1:3306)/app",
			[]string{"tlsCAFile=%2Fvar%2Flib%2Fjust-dashboard%2Ftls%2Fca.pem"}},
	} {
		t.Run(string(c.driver), func(t *testing.T) {
			secure, err := WithServerTLS(c.driver, c.plain, testCAFile)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range c.secure {
				if !strings.Contains(secure, want) {
					t.Errorf("%q lacks %q", secure, want)
				}
			}
			if got := ServerTLSFile(c.driver, secure); got != testCAFile {
				t.Errorf("reads the authority file as %q, want %q", got, testCAFile)
			}
			if ServerTLSFile(c.driver, c.plain) != "" {
				t.Error("a plain connection reads as one set up for TLS")
			}
			info, err := ParseDSN(c.driver, secure)
			if err != nil || info.Host != "127.0.0.1" || info.User == "" && c.driver != DriverRedis || info.Password != "pw" {
				t.Errorf("the account and address are lost: %+v %v", info, err)
			}
			back, err := WithoutServerTLS(c.driver, secure)
			if err != nil {
				t.Fatal(err)
			}
			if ServerTLSFile(c.driver, back) != "" || strings.Contains(back, "tlsCAFile") || strings.Contains(back, "sslrootcert") || strings.Contains(back, "rediss") {
				t.Errorf("TLS is still in %q", back)
			}
		})
	}
	if _, err := WithServerTLS(DriverClickHouse, "clickhouse://x@127.0.0.1/db", testCAFile); err == nil {
		t.Error("an engine with no server TLS was given some")
	}
	if _, err := WithServerTLS(DriverPostgres, "postgres://jd@127.0.0.1/app", ""); err == nil {
		t.Error("TLS was switched on with no authority to verify against")
	}
	if _, err := WithServerTLS(DriverPostgres, "host=127.0.0.1 user=jd", testCAFile); err == nil {
		t.Error("a keyword connection string was edited as a URL")
	}
}

// An application has no copy of the authority. While TLS is only allowed it is
// given the string it had before; once it is required it is given one that uses
// TLS without checking the certificate, where the driver can say so.
func TestApplicationsGetAStringTheyCanUse(t *testing.T) {
	secure := func(driver Driver, plain string) string {
		out, err := WithServerTLS(driver, plain, testCAFile)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	pg := "postgres://jd:pw@db-4.jd.internal:5432/app?sslmode=disable"
	my := "jd:pw@tcp(127.0.0.1:3306)/app"
	mongo := "mongodb://jd:pw@127.0.0.1:27017/app?authSource=admin"
	redis := "redis://:pw@127.0.0.1:6379/0"
	for _, c := range []struct {
		driver Driver
		plain  string
	}{{DriverPostgres, pg}, {DriverMySQL, my}, {DriverMongo, mongo}} {
		allowed := ForApplication(c.driver, secure(c.driver, c.plain), false)
		if ServerTLSFile(c.driver, allowed) != "" || strings.Contains(allowed, "tlsCAFile") || strings.Contains(allowed, "sslrootcert") ||
			strings.Contains(allowed, "verify-full") || strings.Contains(allowed, "tls=true") || strings.Contains(allowed, "skip-verify") {
			t.Errorf("%s allowed: %q still carries TLS", c.driver, allowed)
		}
	}
	got := ForApplication(DriverPostgres, secure(DriverPostgres, pg), true)
	if !strings.Contains(got, "sslmode=require") || strings.Contains(got, "sslrootcert") {
		t.Errorf("Postgres required = %q", got)
	}
	got = ForApplication(DriverMongo, secure(DriverMongo, mongo), true)
	if !strings.Contains(got, "tls=true") || !strings.Contains(got, "tlsInsecure=true") ||
		strings.Contains(got, "tlsCAFile") || !strings.Contains(got, "authSource=admin") {
		t.Errorf("Mongo required = %q", got)
	}
	got = ForApplication(DriverMySQL, secure(DriverMySQL, my), true)
	if got != "jd:pw@tcp(127.0.0.1:3306)/app?tls=skip-verify" {
		t.Errorf("MySQL required = %q", got)
	}
	// Redis is one protocol per port: TLS whether or not it is "required".
	for _, required := range []bool{false, true} {
		got = ForApplication(DriverRedis, secure(DriverRedis, redis), required)
		if !strings.HasPrefix(got, "rediss://") || strings.Contains(got, "tlsCAFile") {
			t.Errorf("Redis (required=%v) = %q", required, got)
		}
	}
	for driver, plain := range map[Driver]string{DriverPostgres: pg, DriverMySQL: my, DriverMongo: mongo, DriverRedis: redis} {
		if ForApplication(driver, plain, true) != plain {
			t.Errorf("%s: a connection without server TLS was changed", driver)
		}
	}
}

// The MySQL driver knows nothing of the authority file, so the dialect turns it
// into a TLS configuration the driver does know.
func TestMySQLDialectRegistersTheAuthority(t *testing.T) {
	ca, caPEM, _ := mustCA(t)
	_ = ca
	file := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(file, caPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	dsn, err := WithServerTLS(DriverMySQL, "jd:pw@tcp(127.0.0.1:3306)/app", file)
	if err != nil {
		t.Fatal(err)
	}
	dialect := mysqlDialect{}
	got := dialect.NormaliseDSN(dsn)
	if strings.Contains(got, "tlsCAFile") || !strings.Contains(got, "tls=jd-ca-") {
		t.Fatalf("normalised to %q", got)
	}
	cfg, err := mysql.ParseDSN(got)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS == nil || cfg.TLS.RootCAs == nil {
		t.Error("the driver would not verify against the authority")
	}
	if cfg.TLS != nil && cfg.TLS.ServerName != "127.0.0.1" {
		t.Errorf("the driver verifies the certificate for %q", cfg.TLS.ServerName)
	}
	plain := "jd:pw@tcp(127.0.0.1:3306)/app"
	if dialect.NormaliseDSN(plain) != plain {
		t.Error("a plain connection string was rewritten")
	}
	missing, _ := WithServerTLS(DriverMySQL, plain, filepath.Join(t.TempDir(), "gone.pem"))
	if dialect.NormaliseDSN(missing) != missing {
		t.Error("an unreadable authority file was papered over")
	}
}

func TestRedisOptionsVerifyAgainstTheAuthority(t *testing.T) {
	_, caPEM, _ := mustCA(t)
	file := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(file, caPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	dsn, err := WithServerTLS(DriverRedis, "redis://:pw@127.0.0.1:6380/3", file)
	if err != nil {
		t.Fatal(err)
	}
	opt, err := redisOptions(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if opt.TLSConfig == nil || opt.TLSConfig.RootCAs == nil || opt.TLSConfig.InsecureSkipVerify {
		t.Fatal("Redis would not verify the server against the authority")
	}
	if opt.TLSConfig.ServerName != "127.0.0.1" || opt.DB != 3 || opt.Password != "pw" {
		t.Errorf("options lost the address or the account: %+v", opt)
	}
	if n, err := redisDatabaseIndex(dsn, ""); err != nil || n != 3 {
		t.Errorf("database index = %d, %v; want 3", n, err)
	}
	gone, _ := WithServerTLS(DriverRedis, "redis://:pw@127.0.0.1:6380/3", filepath.Join(t.TempDir(), "gone.pem"))
	if _, err := redisOptions(gone); err == nil || strings.Contains(err.Error(), "pw") {
		t.Errorf("an unreadable authority file gave %v", err)
	}
}

func mustCA(t *testing.T) (*dbtls.CA, []byte, []byte) {
	t.Helper()
	certPEM, keyPEM, err := dbtls.NewCA(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ca, err := dbtls.LoadCA(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return ca, certPEM, keyPEM
}
