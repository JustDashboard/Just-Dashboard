package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbtls"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Switches a real server of each engine to TLS and back, in containers of its
// own, and holds the dashboard to what an operator would see: the saved
// connection verifies the certificate, a client with the authority's file
// connects, plaintext is refused once it is required, the certificate on the
// wire is replaced by a renewal without the server going down, and switching it
// off puts the server back as it was.
//
// JD_DB_TLS_LIVE=1 on a host with Docker and the engines' images already pulled;
// JD_DB_TLS_ENGINES narrows it to some of them (a comma-separated list of
// the subtests' names). Each server it starts is removed with its volumes.
func TestLiveDatabaseServerTLS(t *testing.T) {
	if os.Getenv("JD_DB_TLS_LIVE") != "1" {
		t.Skip("set JD_DB_TLS_LIVE=1 on a Docker host")
	}
	// mariadb-10.11 is the oldest MariaDB the template offers that is not the
	// 11.x line: refusing plaintext has to work on it as much as on 11.4.
	engines := []struct{ name, key, version string }{
		{"postgres", "postgres", "17"}, {"redis", "redis", "8"}, {"mysql", "mysql", "8.4"},
		{"mariadb", "mariadb", "11.4"}, {"mariadb-10.11", "mariadb", "10.11"}, {"mongodb", "mongodb", "8"},
		{"valkey", "valkey", "8"},
	}
	only := map[string]bool{}
	for _, key := range strings.Split(os.Getenv("JD_DB_TLS_ENGINES"), ",") {
		if key = strings.TrimSpace(key); key != "" {
			only[key] = true
		}
	}
	for _, engine := range engines {
		if len(only) > 0 && !only[engine.name] {
			continue
		}
		t.Run(engine.name, func(t *testing.T) { liveServerTLS(t, engine.name, engine.key, engine.version) })
	}
}

func tlsLiveRouter(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s := testServer(t)
	s.modules.docker = dockerx.New("unix:///var/run/docker.sock")
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "tester"}, Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	s.mountDatabaseRoutes(r)
	return s, r
}

func liveServerTLS(t *testing.T, label, key, version string) {
	s, router := tlsLiveRouter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	name := "jdt-db-server-tls-" + strings.ReplaceAll(label, ".", "-")
	t.Cleanup(func() {
		clean, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		_ = s.modules.docker.RemoveContainer(clean, name, true, true)
		_ = s.modules.docker.RemoveVolume(clean, name+"-data", true)
		_ = s.modules.docker.RemoveVolume(clean, dbTLSVolume(name), true)
	})

	body, _ := json.Marshal(map[string]string{"engine": key, "name": name, "version": version, "database": "app", "exposure": "local"})
	if rec := do(t, router, http.MethodPost, "/databases/provision", string(body)); rec.Code != http.StatusAccepted {
		t.Fatalf("provision: %d %s", rec.Code, rec.Body)
	}
	// The container carries the label and memory limit this machine's rules ask of one.
	spec, err := s.modules.docker.SpecOf(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	spec.Labels = append(spec.Labels, dockerx.LabelSpec{Name: "jdt-owner", Value: "db-server-tls"})
	spec.Limits.MemoryMB = 512
	if _, err := s.modules.docker.Recreate(ctx, name, dockerx.RecreateOptions{Spec: spec}, nil); err != nil {
		t.Fatalf("labelling the container: %v", err)
	}
	var conn dbConnection
	for deadline := time.Now().Add(3 * time.Minute); ; time.Sleep(2 * time.Second) {
		adopted := do(t, router, http.MethodPost, "/databases/adopt", fmt.Sprintf(`{"container":%q}`, name))
		if adopted.Code >= 200 && adopted.Code < 300 {
			if err := json.Unmarshal(adopted.Body.Bytes(), &conn); err != nil {
				t.Fatal(err)
			}
			if ping := do(t, router, http.MethodGet, pathf("/databases/%d/ping", conn.ID), ""); strings.Contains(ping.Body.String(), `"ok":true`) {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server never answered; last adopt %d", adopted.Code)
		}
	}
	id := conn.ID
	tlsPath := pathf("/databases/%d/tls", id)
	ready := func(label string) {
		t.Helper()
		ping := do(t, router, http.MethodGet, pathf("/databases/%d/ping", id), "")
		if !strings.Contains(ping.Body.String(), `"ok":true`) {
			t.Fatalf("%s: the saved connection does not connect: %s", label, ping.Body)
		}
	}
	status := func() dbTLSStatus {
		t.Helper()
		rec := do(t, router, http.MethodGet, tlsPath, "")
		var st dbTLSStatus
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &st) != nil {
			t.Fatalf("status: %d %s", rec.Code, rec.Body)
		}
		return st
	}
	saved := func() (string, string) {
		_, dsn, err := s.dbConnRow(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return dsn, dbx.ServerTLSFile(conn.Driver, dsn)
	}
	plaintextWorks := func() bool {
		dsn, _ := saved()
		plain, err := dbx.WithoutServerTLS(conn.Driver, dsn)
		if err != nil {
			t.Fatal(err)
		}
		return s.dialDatabase(ctx, conn.Driver, plain) == nil
	}
	hostPort := func() string {
		dsn, _ := saved()
		info, _ := dbx.ParseDSN(conn.Driver, dsn)
		return net.JoinHostPort(info.Host, info.Port)
	}

	st := status()
	if !st.Supported || st.Enabled || st.Authority != nil {
		t.Fatalf("before: %+v", st)
	}
	if !plaintextWorks() {
		t.Fatal("before: plaintext does not connect")
	}

	// On, with plaintext still allowed where the engine can.
	if rec := do(t, router, http.MethodPut, tlsPath, `{"enabled":true,"require":false}`); rec.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	st = status()
	engine := dbTLSEngines[key]
	if !st.Enabled || !st.Verified || st.Serial == "" || st.Authority == nil || st.Require != !engine.optional {
		t.Fatalf("after enabling: %+v", st)
	}
	for _, want := range []string{"127.0.0.1", "::1", "localhost", databaseDNSName(id), name} {
		if !contains(st.Names, want) {
			t.Errorf("certificate names %v lack %s", st.Names, want)
		}
	}
	_, file := saved()
	if file == "" {
		t.Fatal("the saved connection does not name the authority's file")
	}
	ready("TLS on")
	if got := servedSerial(t, key, hostPort()); got != st.Serial {
		t.Errorf("the server presents certificate %s, the dashboard recorded %s", got, st.Serial)
	}
	if engine.optional && !plaintextWorks() {
		t.Error("plaintext was refused when it was only allowed")
	}
	applicationString(t, router, s, conn.Driver, id, st, "allowed")
	if !engine.optional && plaintextWorks() {
		t.Error("plaintext connects to a port that should speak TLS only")
	}
	if rec := do(t, router, http.MethodGet, "/databases/tls/authority", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "BEGIN CERTIFICATE") {
		t.Errorf("authority download: %d", rec.Code)
	}
	// The verification is real: against another authority the same server is refused.
	other := t.TempDir() + "/other.pem"
	if err := os.WriteFile(other, otherAuthorityPEM(t), 0o644); err != nil {
		t.Fatal(err)
	}
	dsn, _ := saved()
	if wrong, err := dbx.WithServerTLS(conn.Driver, dsn, other); err != nil || s.dialDatabase(ctx, conn.Driver, wrong) == nil {
		t.Errorf("a connection verifying against another authority connected (%v)", err)
	}

	// Refusing plaintext.
	if engine.optional {
		if rec := do(t, router, http.MethodPut, tlsPath, `{"enabled":true,"require":true}`); rec.Code != http.StatusOK {
			t.Fatalf("require: %d %s", rec.Code, rec.Body)
		}
		if st = status(); !st.Require {
			t.Fatalf("after requiring: %+v", st)
		}
		ready("TLS required")
		if plaintextWorks() {
			t.Error("plaintext connects to a server that requires TLS")
		}
		applicationString(t, router, s, conn.Driver, id, st, "required")
		// The server is not forgotten while it speaks TLS.
		if rec := do(t, router, http.MethodDelete, pathf("/databases/%d", id), ""); rec.Code != http.StatusConflict {
			t.Errorf("forgetting a connection whose server speaks TLS answered %d", rec.Code)
		}
	}

	// Renewal replaces the certificate under the running server.
	before := st.Serial
	started := containerStarted(t, s, ctx, name)
	if rec := do(t, router, http.MethodPost, tlsPath+"/renew", ""); rec.Code != http.StatusOK {
		t.Fatalf("renew: %d %s", rec.Code, rec.Body)
	}
	st = status()
	if st.Serial == before || st.RenewedAt == nil {
		t.Fatalf("renewal kept certificate %s: %+v", before, st)
	}
	ready("renewed")
	if got := servedSerial(t, key, hostPort()); got != st.Serial {
		t.Errorf("after renewal the server presents %s, the dashboard recorded %s", got, st.Serial)
	}
	if !containerStarted(t, s, ctx, name).Equal(started) {
		t.Error("renewal restarted the server")
	}

	// The background renewal takes up a certificate inside its window.
	before = st.Serial
	if _, err := s.Store.DB.Exec(`UPDATE db_server_tls SET not_after = ? WHERE connection_id = ?`, time.Now().Add(48*time.Hour).Unix(), id); err != nil {
		t.Fatal(err)
	}
	s.renewDueDBTLS(ctx)
	if st = status(); st.Serial == before {
		t.Fatal("the certificate inside its renewal window was not renewed")
	}
	ready("renewed by the schedule")
	var actor, action string
	var success bool
	if err := s.Store.DB.QueryRow(`SELECT actor, action, success FROM audit_log WHERE action = 'database.tls.renew' AND actor = 'system'`).
		Scan(&actor, &action, &success); err != nil || !success {
		t.Errorf("the scheduled renewal left no audit entry: %v", err)
	}
	var changes int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'database.tls.change'`).Scan(&changes); err != nil || changes == 0 {
		t.Errorf("switching TLS left no audit entry: %d %v", changes, err)
	}

	// Off: the server and the saved connection are as they were.
	if rec := do(t, router, http.MethodPut, tlsPath, `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	st = status()
	if st.Enabled || st.Verified || st.Serial != "" {
		t.Fatalf("after disabling: %+v", st)
	}
	ready("TLS off")
	if _, file := saved(); file != "" {
		t.Error("the saved connection still names the authority's file")
	}
	after, err := s.modules.docker.SpecOf(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if hasDBTLSMount(after) || strings.Join(after.Command, "\x00") != strings.Join(spec.Command, "\x00") {
		t.Errorf("the container was not put back: mounts %v command %v, was %v", after.Mounts, after.Command, spec.Command)
	}
	if exists, _ := s.modules.docker.VolumeExists(ctx, dbTLSVolume(name)); exists {
		t.Error("the certificate volume was left behind")
	}
}

// applicationString holds what a linked program is given to what a program with
// no copy of the authority can use: no path of this machine's, the string it had
// before TLS while TLS is only allowed, and TLS without a check once it is
// required (Redis, with no such word in a URL, needs the authority).
func applicationString(t *testing.T, router http.Handler, s *Server, driver dbx.Driver, id int64, st dbTLSStatus, mode string) {
	t.Helper()
	for _, target := range []string{"host", "public"} {
		rec := do(t, router, http.MethodGet, pathf("/databases/%d/url", id)+"?target="+target, "")
		var got struct {
			URL string `json:"url"`
		}
		if target == "public" && rec.Code != http.StatusOK {
			continue // no public address on this machine
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("%s URL (%s): %d %s", target, mode, rec.Code, rec.Body)
		}
		if strings.Contains(got.URL, "ca.pem") || strings.Contains(got.URL, "tlsCAFile") || strings.Contains(got.URL, "sslrootcert") {
			t.Errorf("%s URL (%s) carries this machine's authority file: %s", target, mode, got.URL)
		}
		if target != "host" {
			continue
		}
		dialled := s.dialDatabase(context.Background(), driver, got.URL)
		switch {
		case driver == dbx.DriverRedis:
			if !strings.HasPrefix(got.URL, "rediss://") {
				t.Errorf("Redis application string (%s) does not use TLS: %s", mode, got.URL)
			}
			if st.Applications != "needsCA" {
				t.Errorf("Redis applications = %q", st.Applications)
			}
		case mode == "allowed":
			if dialled != nil {
				t.Errorf("the string an application gets while TLS is allowed does not connect (%v): %s", dialled, got.URL)
			}
			if st.Applications != "plain" {
				t.Errorf("applications = %q while allowed", st.Applications)
			}
		default:
			if dialled != nil {
				t.Errorf("the string an application gets once TLS is required does not connect (%v): %s", dialled, got.URL)
			}
			if st.Applications != "unverified" {
				t.Errorf("applications = %q once required", st.Applications)
			}
		}
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func containerStarted(t *testing.T, s *Server, ctx context.Context, name string) time.Time {
	t.Helper()
	detail, err := s.modules.docker.Inspect(ctx, name)
	if err != nil || detail.StartedAt == nil {
		t.Fatalf("inspecting %s: %v", name, err)
	}
	return *detail.StartedAt
}

func otherAuthorityPEM(t *testing.T) []byte {
	t.Helper()
	certPEM, _, err := dbtls.NewCA(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return certPEM
}

// servedSerial is the serial number of the certificate a server presents on a
// new connection, read the way a client would: straight TLS for Redis and
// MongoDB, and the protocol's own upgrade for the others, which openssl speaks.
func servedSerial(t *testing.T, key, addr string) string {
	t.Helper()
	var leaf *x509.Certificate
	switch key {
	case "redis", "valkey", "mongodb":
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatalf("TLS handshake with %s: %v", addr, err)
		}
		defer conn.Close()
		leaf = conn.ConnectionState().PeerCertificates[0]
	default:
		openssl, err := exec.LookPath("openssl")
		if err != nil {
			t.Skip("openssl is needed to read the certificate a SQL server presents")
		}
		proto := "postgres"
		if key == "mysql" || key == "mariadb" {
			proto = "mysql"
		}
		cmd := exec.Command(openssl, "s_client", "-starttls", proto, "-connect", addr)
		cmd.Stdin = strings.NewReader("")
		var out bytes.Buffer
		cmd.Stdout = &out
		_ = cmd.Run()
		block, _ := pem.Decode(out.Bytes()[max(0, bytes.Index(out.Bytes(), []byte("-----BEGIN CERTIFICATE-----"))):])
		if block == nil {
			t.Fatalf("openssl read no certificate from %s: %.300s", addr, out.String())
		}
		if leaf, err = x509.ParseCertificate(block.Bytes); err != nil {
			t.Fatal(err)
		}
	}
	return strings.ToUpper(leaf.SerialNumber.Text(16))
}
