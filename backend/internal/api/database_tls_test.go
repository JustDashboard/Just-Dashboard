package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbtls"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// A template is offered TLS exactly when the capability table says so: the
// section on the page is drawn from the one, and acts through the other.
func TestTLSProfilesAgreeWithTheCapabilityTable(t *testing.T) {
	for _, key := range provisionOrder {
		tmpl := provisionTemplates[key]
		_, has := dbTLSEngines[key]
		if got := dbx.Capable(tmpl.driver, tmpl.flavor, "serverTLS"); got != has {
			t.Errorf("%s: the capability table says %v and the profiles say %v", key, got, has)
		}
	}
}

func TestEveryTemplateImageIsTakenBackToItsTemplate(t *testing.T) {
	for key := range dbTLSEngines {
		for _, v := range provisionTemplates[key].versions {
			got, _, version, ok := provisionTemplateOf(v.image)
			if !ok || got != key || version.id != v.id {
				t.Errorf("%s resolves to %q %q (%v), want %s %s", v.image, got, version.id, ok, key, v.id)
			}
		}
	}
	if _, _, _, ok := provisionTemplateOf("postgres:16-alpine-with-my-patch"); ok {
		t.Error("an image that is not a template's was taken for one")
	}
}

func tlsSpec(command ...string) *dockerx.ContainerSpec {
	return &dockerx.ContainerSpec{Entrypoint: []string{"docker-entrypoint.sh"}, Command: command}
}

// What a start command is before TLS, and after, and back again: the original
// is carried inside the new one, so nothing somebody added is lost.
func TestStartCommandSurvivesSwitchingTLSOnAndOff(t *testing.T) {
	for key, engine := range dbTLSEngines {
		tmpl := provisionTemplates[key]
		var original []string
		if engine.redisServer != "" {
			original = []string{"sh", "-c", tmpl.bootstrap}
		} else {
			original = []string{engine.servers[0], "--max-connections=50"}
		}
		got, on, err := engine.readCommand(tmpl, tlsSpec(original...))
		if err != nil || on {
			t.Fatalf("%s: a plain command read as on=%v err=%v", key, on, err)
		}
		for _, require := range []bool{false, true} {
			secured := engine.command(got, require)
			read, on, err := engine.readCommand(tmpl, tlsSpec(secured...))
			if err != nil || !on {
				t.Fatalf("%s: a TLS command read as on=%v err=%v", key, on, err)
			}
			if engine.redisServer == "" && strings.Join(read, " ") != strings.Join(original, " ") {
				t.Errorf("%s: the original command came back as %v, want %v", key, read, original)
			}
			if back := engine.plain(tmpl, read); strings.Join(back, "\x00") != strings.Join(original, "\x00") {
				t.Errorf("%s: switched off, the command is %v, want %v", key, back, original)
			}
		}
	}
}

func TestStartCommandsThatWereChangedAreNotWrapped(t *testing.T) {
	pg := dbTLSEngines["postgres"]
	tmpl := provisionTemplates["postgres"]
	for name, spec := range map[string]*dockerx.ContainerSpec{
		"another program":    tlsSpec("bash", "-c", "postgres"),
		"no command":         tlsSpec(),
		"another entrypoint": {Entrypoint: []string{"/my-entry.sh"}, Command: []string{"postgres"}},
		"no entrypoint":      {Command: []string{"postgres"}},
	} {
		if _, _, err := pg.readCommand(tmpl, spec); err == nil {
			t.Errorf("%s: a command nobody recognises was accepted", name)
		}
	}
	valkey := dbTLSEngines["valkey"]
	tini := &dockerx.ContainerSpec{Entrypoint: []string{"tini", "--", "docker-entrypoint.sh"}, Command: []string{"sh", "-c", provisionTemplates["valkey"].bootstrap}}
	if _, on, err := valkey.readCommand(provisionTemplates["valkey"], tini); err != nil || on {
		t.Errorf("an image that starts its entrypoint under tini was refused: on=%v err=%v", on, err)
	}
	redis := dbTLSEngines["redis"]
	if _, _, err := redis.readCommand(provisionTemplates["redis"], tlsSpec("redis-server", "--appendonly", "yes")); err == nil {
		t.Error("a Redis whose start script was replaced was accepted")
	}
}

func TestTLSScriptsCarryTheArgumentsAndNoSecret(t *testing.T) {
	pg := dbTLSEngines["postgres"]
	plain, required := pg.wrapScript(false), pg.wrapScript(true)
	for _, want := range []string{"ssl=on", "ssl_cert_file=/jd-tls/server.crt", "ssl_key_file=/jd-tls/server.key", `exec docker-entrypoint.sh "$@"`, "chown postgres:postgres /jd-tls/server.key"} {
		if !strings.Contains(plain, want) {
			t.Errorf("PostgreSQL script lacks %q: %s", want, plain)
		}
	}
	if strings.Contains(plain, "hba_file") || !strings.Contains(required, "hba_file=/jd-tls/pg_hba.conf") {
		t.Error("only the script that refuses plaintext names the rules file")
	}
	if args := dbTLSEngines["mysql"].args(true); !strings.Contains(strings.Join(args, " "), "--require-secure-transport=ON") {
		t.Errorf("MySQL arguments that require TLS = %v", args)
	}
	if args := dbTLSEngines["mysql"].args(false); strings.Contains(strings.Join(args, " "), "require") {
		t.Errorf("MySQL arguments that allow plaintext = %v", args)
	}
	mongo := strings.Join(dbTLSEngines["mongodb"].args(true), " ")
	if !strings.Contains(mongo, "--tlsMode requireTLS") || !strings.Contains(mongo, "--tlsAllowConnectionsWithoutCertificates") {
		t.Errorf("MongoDB arguments = %s", mongo)
	}
	if !strings.Contains(strings.Join(dbTLSEngines["mongodb"].args(false), " "), "--tlsMode preferTLS") {
		t.Error("MongoDB that allows plaintext is not told preferTLS")
	}
	script := redisTLSScript("redis-server", "redis")
	for _, want := range []string{"tls-port 6379", "port 0", "tls-auth-clients no", `"$REDIS_PASSWORD" > "$conf"`, "rm -f"} {
		if !strings.Contains(script, want) {
			t.Errorf("Redis script lacks %q", want)
		}
	}
	for key, engine := range dbTLSEngines {
		for _, text := range []string{engine.reload, engine.wrapScriptOrEmpty()} {
			if strings.Contains(text, "password=") || strings.Contains(text, "pw12345") {
				t.Errorf("%s: a script carries a secret: %s", key, text)
			}
		}
	}
}

// wrapScriptOrEmpty lets the loop above read every profile's start script.
func (e dbTLSEngine) wrapScriptOrEmpty() string {
	if e.args == nil {
		return redisTLSScript(e.redisServer, e.account)
	}
	return e.wrapScript(true)
}

func TestCertificateIsDeliveredInTheFormEachEngineReads(t *testing.T) {
	certPEM, keyPEM, err := dbtls.NewCA(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ca, err := dbtls.LoadCA(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.Issue([]string{"127.0.0.1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for key, engine := range dbTLSEngines {
		files := map[string]dbTLSFile{}
		for _, f := range engine.files(leaf, certPEM) {
			files[f.name] = f
		}
		if files["ca.crt"].mode != 0o644 || !strings.Contains(string(files["ca.crt"].content), "CERTIFICATE") {
			t.Errorf("%s: the authority is not delivered as a readable certificate", key)
		}
		keyFile := files[engine.keyFile]
		if keyFile.mode != 0o600 || !strings.Contains(string(keyFile.content), "PRIVATE KEY") {
			t.Errorf("%s: the key file %s is not private (%o)", key, engine.keyFile, keyFile.mode)
		}
		if engine.keyFile == "server.pem" {
			if !strings.Contains(string(keyFile.content), "BEGIN CERTIFICATE") {
				t.Errorf("%s: the combined file holds no certificate", key)
			}
			if _, ok := files["server.crt"]; ok {
				t.Errorf("%s: a separate certificate was written beside the combined file", key)
			}
		} else if files["server.crt"].mode != 0o644 || strings.Contains(string(files["server.crt"].content), "PRIVATE KEY") {
			t.Errorf("%s: the certificate file is wrong", key)
		}
		extra := engine.extraFiles(true)
		if engine.hba {
			if len(extra) != 1 || extra[0].name != "pg_hba.conf" || !strings.Contains(string(extra[0].content), "hostnossl") {
				t.Errorf("%s: the rules that refuse plaintext are not delivered: %v", key, extra)
			}
		} else if len(extra) != 0 {
			t.Errorf("%s: a rules file was delivered to an engine with no use for one", key)
		}
		if len(engine.extraFiles(false)) != 0 {
			t.Errorf("%s: rules were delivered for a server that allows plaintext", key)
		}
	}
}

// MariaDB has had require_secure_transport since 10.5.2, and every release the
// templates offer is later than that, so every one can refuse plaintext.
func TestEveryMariaDBReleaseTheTemplateOffersCanRefusePlaintext(t *testing.T) {
	for _, v := range provisionTemplates["mariadb"].versions {
		major, minor, _ := strings.Cut(v.id, ".")
		maj, _ := strconv.Atoi(major)
		min, _ := strconv.Atoi(minor)
		if maj < 10 || (maj == 10 && min < 6) {
			t.Errorf("MariaDB %s is offered and is older than 10.6", v.id)
		}
	}
	if args := strings.Join(dbTLSEngines["mariadb"].args(true), " "); !strings.Contains(args, "--require-secure-transport=ON") {
		t.Errorf("MariaDB arguments that require TLS = %s", args)
	}
}

// A renewal writes beside the pair the server reads and swaps them in with one
// command, so a failure while writing leaves the old pair as it was.
func TestRenewalSwapsTheFilesInWithOneCommand(t *testing.T) {
	certPEM, keyPEM, err := dbtls.NewCA(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ca, err := dbtls.LoadCA(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.Issue([]string{"127.0.0.1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for key, engine := range dbTLSEngines {
		files := engine.files(leaf, certPEM)
		swap := engine.swapScript(files)
		if !strings.HasPrefix(swap, "set -e;") {
			t.Errorf("%s: a failing step does not stop the swap: %s", key, swap)
		}
		if strings.Count(swap, "mv -f") != len(files) {
			t.Errorf("%s: %d files and %d moves: %s", key, len(files), strings.Count(swap, "mv -f"), swap)
		}
		if !strings.Contains(swap, "chown "+engine.account+":"+engine.account+" /jd-tls/"+engine.keyFile+".new") {
			t.Errorf("%s: the key is moved in before it is the server's: %s", key, swap)
		}
		for _, f := range files {
			if !strings.Contains(swap, "/jd-tls/"+f.name+".new /jd-tls/"+f.name) {
				t.Errorf("%s: %s is not moved into place: %s", key, f.name, swap)
			}
		}
		if discard := engine.discardScript(files); !strings.HasPrefix(discard, "rm -f ") || strings.Count(discard, ".new") != len(files) {
			t.Errorf("%s: discard = %s", key, discard)
		}
	}
}

func TestAFailedPutBackIsSaidPlainly(t *testing.T) {
	if got := putBackWords("shop-db", nil); got != "It was put back as it was." {
		t.Errorf("put back: %q", got)
	}
	got := putBackWords("shop-db", errors.New("could not set it aside"))
	for _, want := range []string{"may be unreachable", "shop-db", "_jd_replaced_", "could not set it aside"} {
		if !strings.Contains(got, want) {
			t.Errorf("a failed put back does not say %q: %s", want, got)
		}
	}
	if strings.Contains(got, "It was put back as it was") {
		t.Error("a failed put back claims to have worked")
	}
}

// A linked application is given the string it had until TLS is required, and
// then one that uses TLS without checking what it has no copy of the authority
// to check.
func TestApplicationStringsFollowWhetherTLSIsRequired(t *testing.T) {
	s, _, id := redisRouter(t, auth.RoleAdmin, nowhere)
	ctx := t.Context()
	conn := &dbConnection{ID: id, Driver: dbx.DriverPostgres}
	secure := "postgres://jd:pw@127.0.0.1:5433/app?sslmode=verify-full&sslrootcert=/var/lib/just-dashboard/tls/ca.pem"
	plain := "postgres://jd:pw@127.0.0.1:5433/app?sslmode=disable"
	if got := s.applicationDSN(ctx, conn, plain); got != plain {
		t.Errorf("a connection without TLS was changed: %s", got)
	}
	if err := s.saveDBTLSRow(ctx, id, &dbTLSRow{Container: "c", Names: []string{"127.0.0.1"}, IssuedAt: time.Now(), NotAfter: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got := s.applicationDSN(ctx, conn, secure); got != plain {
		t.Errorf("TLS allowed: application string = %s, want the one it had", got)
	}
	if err := s.saveDBTLSRow(ctx, id, &dbTLSRow{Container: "c", Require: true, Names: []string{"127.0.0.1"}, IssuedAt: time.Now(), NotAfter: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	got := s.applicationDSN(ctx, conn, secure)
	if !strings.Contains(got, "sslmode=require") || strings.Contains(got, "sslrootcert") || strings.Contains(got, "ca.pem") {
		t.Errorf("TLS required: application string = %s", got)
	}
	for require, want := range map[bool]string{false: "plain", true: "unverified"} {
		if got := applicationsGet(require, dbx.DriverPostgres); got != want {
			t.Errorf("applications (require=%v) = %s, want %s", require, got, want)
		}
	}
	if applicationsGet(true, dbx.DriverRedis) != "needsCA" || applicationsGet(false, dbx.DriverRedis) != "plain" {
		t.Error("a Redis client with TLS required is not told it needs the authority")
	}
}

// Forgetting a connection while its server still speaks TLS would leave the
// server with a certificate nothing renews, and a connection added again would
// dial plain text.
func TestAConnectionIsNotForgottenWhileItsServerSpeaksTLS(t *testing.T) {
	s, h, id := redisRouter(t, auth.RoleAdmin, nowhere)
	if err := s.saveDBTLSRow(t.Context(), id, &dbTLSRow{Container: "c", Names: []string{"127.0.0.1"}, IssuedAt: time.Now(), NotAfter: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/databases/"+strconv.FormatInt(id, 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "turn TLS off") {
		t.Fatalf("forgetting answered %d %s", rec.Code, rec.Body.String())
	}
	var kept int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_connections WHERE id = ?`, id).Scan(&kept); err != nil || kept != 1 {
		t.Errorf("the connection was removed: %d %v", kept, err)
	}
	if _, err := s.Store.DB.Exec(`DELETE FROM db_server_tls WHERE connection_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/databases/"+strconv.FormatInt(id, 10), nil))
	if rec.Code != http.StatusNoContent {
		t.Errorf("forgetting after TLS was turned off answered %d %s", rec.Code, rec.Body.String())
	}
}

// A certificate that already ends where the authority ends cannot be given a
// longer life, so the schedule does not renew it on that account, every pass.
func TestACertificateCappedByTheAuthorityIsNotRenewedForever(t *testing.T) {
	s := testServer(t)
	if _, _, _, err := s.loadDBTLSAuthority(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('shop','postgres','x',0)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	// The authority has ten days left, so any certificate is inside its window.
	end := time.Now().Add(10 * 24 * time.Hour).Truncate(time.Second)
	if _, err := s.Store.DB.Exec(`UPDATE db_tls_ca SET not_after = ?`, end.Unix()); err != nil {
		t.Fatal(err)
	}
	if got := s.dbTLSRenewalReason(t.Context(), id, end.Add(-5*24*time.Hour)); got != "expiry" {
		t.Errorf("a certificate ending before the authority has reason %q, want expiry", got)
	}
	// Ending exactly where the authority does: nothing a renewal can improve.
	if got := s.dbTLSRenewalReason(t.Context(), id, end); got != "" {
		t.Errorf("a certificate at the authority's end has reason %q, want none", got)
	}
}

func tlsTestTarget(t *testing.T, driver dbx.Driver, key, image, dsn, bindingIP string) *dbTLSTarget {
	t.Helper()
	info, err := dbx.ParseDSN(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	return &dbTLSTarget{
		conn: &dbConnection{ID: 7, Driver: driver, Name: "shop"}, dsn: dsn, info: info, key: key,
		server: &dbServer{
			container: &dockerx.Container{Name: image},
			binding:   &dockerx.Port{IP: bindingIP, PublicPort: uint16(atoiPort(info.Port)), PrivatePort: 5432},
		},
	}
}

func atoiPort(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func TestCertificateNamesCoverEveryAddressItIsReachedAt(t *testing.T) {
	local := tlsTestTarget(t, dbx.DriverPostgres, "postgres", "jd-postgres", "postgres://jd:pw@127.0.0.1:5433/app", "127.0.0.1")
	names, err := dbtls.Names(dbTLSNames(local))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1", "::1", "db-7.jd.internal", "jd-postgres", "localhost"}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Errorf("names = %v, want %v", names, want)
	}
	// An underscore is a legal container name and not a legal host name; the
	// certificate leaves it out rather than being refused whole.
	local.server.container.Name = "jd_postgres"
	if got := strings.Join(dbTLSNames(local), " "); strings.Contains(got, "jd_postgres") {
		t.Errorf("an invalid host name was offered: %s", got)
	}
	if _, err := dbtls.Names(dbTLSNames(local)); err != nil {
		t.Errorf("the names for an underscored container cannot be issued: %v", err)
	}
	private := tlsTestTarget(t, dbx.DriverPostgres, "postgres", "jd-postgres", "postgres://jd:pw@127.0.0.1:5433/app", "10.0.0.5")
	if got := dbTLSNames(private); !strings.Contains(strings.Join(got, " "), "10.0.0.5") {
		t.Errorf("the address a private binding listens on is not named: %v", got)
	}
}

func TestClientStringsCarryTheRightModeAndNeverThePassword(t *testing.T) {
	const password = "hunter2-hunter2"
	for _, c := range []struct {
		driver dbx.Driver
		key    string
		dsn    string
		want   []string
	}{
		{dbx.DriverPostgres, "postgres", "postgres://jd:" + password + "@127.0.0.1:5433/app?sslmode=verify-full&sslrootcert=/x/ca.pem",
			[]string{"sslmode=verify-full", "sslrootcert=ca.pem", "jd:<password>@127.0.0.1:5433/app", "psql"}},
		{dbx.DriverMySQL, "mysql", "jd:" + password + "@tcp(127.0.0.1:3307)/app",
			[]string{"--ssl-mode=VERIFY_IDENTITY", "--ssl-ca=ca.pem", "--port=3307"}},
		{dbx.DriverMySQL, "mariadb", "jd:" + password + "@tcp(127.0.0.1:3307)/app",
			[]string{"mariadb", "--ssl-verify-server-cert", "--ssl-ca=ca.pem"}},
		{dbx.DriverMongo, "mongodb", "mongodb://jd:" + password + "@127.0.0.1:27018/app?authSource=admin",
			[]string{"tls=true", "tlsCAFile=ca.pem", "authSource=admin", "mongosh"}},
		{dbx.DriverRedis, "redis", "rediss://:" + password + "@127.0.0.1:6380/0",
			[]string{"rediss://:<password>@127.0.0.1:6380/0", "--tls --cacert ca.pem", "redis-cli"}},
		{dbx.DriverRedis, "valkey", "rediss://:" + password + "@127.0.0.1:6380/0", []string{"valkey-cli"}},
	} {
		target := tlsTestTarget(t, c.driver, c.key, "c", c.dsn, "127.0.0.1")
		client := dbTLSClientFor(target, exposureLocal)
		all := client.URL + "\n" + client.Command
		for _, w := range c.want {
			if !strings.Contains(all, w) {
				t.Errorf("%s: client strings lack %q:\n%s", c.key, w, all)
			}
		}
		if strings.Contains(all, password) {
			t.Errorf("%s: the password is in the client strings", c.key)
		}
		if client.CAFile != "ca.pem" || client.Host != "127.0.0.1" {
			t.Errorf("%s: client = %+v", c.key, client)
		}
	}
}

func TestAuthorityIsMadeOnceAndItsKeyIsSealed(t *testing.T) {
	s := testServer(t)
	ctx := t.Context()
	if a, err := s.readDBTLSAuthority(ctx); err != nil || a != nil {
		t.Fatalf("an authority exists before one was made: %v %v", a, err)
	}
	if _, _, _, err := s.loadDBTLSAuthority(ctx, false); err == nil {
		t.Fatal("an authority was loaded that does not exist")
	}
	ca, first, made, err := s.loadDBTLSAuthority(ctx, true)
	if err != nil || !made || ca == nil {
		t.Fatalf("authority not made: made=%v err=%v", made, err)
	}
	_, second, made, err := s.loadDBTLSAuthority(ctx, true)
	if err != nil || made || second.Fingerprint != first.Fingerprint {
		t.Fatalf("a second load made another authority: made=%v err=%v", made, err)
	}
	var sealed, cert string
	if err := s.Store.DB.QueryRow(`SELECT key_enc, cert_pem FROM db_tls_ca`).Scan(&sealed, &cert); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "PRIVATE KEY") || strings.Contains(sealed, "BEGIN") {
		t.Error("the authority's key is stored in the clear")
	}
	if opened, err := s.Sealer.Open(sealed); err != nil || !strings.Contains(opened, "PRIVATE KEY") {
		t.Errorf("the sealed key does not open with the master key: %v", err)
	}
	if strings.Contains(cert, "PRIVATE KEY") {
		t.Error("the certificate column holds a key")
	}
	other, err := auth.NewSealer(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	s.Sealer = other
	if _, _, _, err := s.loadDBTLSAuthority(ctx, true); err == nil {
		t.Error("the authority opened under another master key")
	}
}

func TestAuthorityFileIsWrittenAndMended(t *testing.T) {
	s := testServer(t)
	_, authority, _, err := s.loadDBTLSAuthority(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	file, err := s.dbTLSCAFile(authority.CertPEM)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("authority file: %v %v", info, err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	s.mendDBTLSAuthorityFile(t.Context())
	got, err := os.ReadFile(file)
	if err != nil || string(got) != string(authority.CertPEM) {
		t.Errorf("the file was not written back: %v", err)
	}
}

func TestCertificateRowsRoundTrip(t *testing.T) {
	s := testServer(t)
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('shop','postgres','x',0)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if row, err := s.dbTLSRow(t.Context(), id); err != nil || row != nil {
		t.Fatalf("a row exists for a connection with no TLS: %v %v", row, err)
	}
	want := &dbTLSRow{
		Container: "jd-postgres", Require: true, Names: []string{"127.0.0.1", "localhost"}, Serial: "AB12",
		IssuedAt: time.Unix(1_800_000_000, 0).UTC(), NotAfter: time.Unix(1_831_000_000, 0).UTC(),
	}
	for i := 0; i < 2; i++ {
		if err := s.saveDBTLSRow(t.Context(), id, want); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.dbTLSRow(t.Context(), id)
	if err != nil || got == nil || !got.Require || got.Serial != "AB12" || strings.Join(got.Names, ",") != "127.0.0.1,localhost" ||
		!got.NotAfter.Equal(want.NotAfter) || !got.RenewedAt.IsZero() {
		t.Fatalf("row = %+v, %v", got, err)
	}
	if _, err := s.Store.DB.Exec(`DELETE FROM db_connections WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if row, _ := s.dbTLSRow(t.Context(), id); row != nil {
		t.Error("a certificate outlived the connection it was for")
	}
}

func tlsRoute(t *testing.T, role auth.Role, method, path, body string) (*Server, *httptest.ResponseRecorder, int64) {
	t.Helper()
	s, h, id := redisRouter(t, role, nowhere)
	req := httptest.NewRequest(method, strings.Replace(path, "{id}", itoa(id), 1), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return s, rec, id
}

// The routes are an administrator's, and the change is one only a role that may
// destroy takes: it recreates the container.
func TestTLSRoutesAskForTheRightRoles(t *testing.T) {
	for _, c := range []struct {
		method, path, body string
		role               auth.Role
		want               int
	}{
		{http.MethodGet, "/databases/{id}/tls", "", auth.RoleReadOnly, http.StatusForbidden},
		{http.MethodGet, "/databases/{id}/tls", "", auth.RoleLimited, http.StatusForbidden},
		{http.MethodPut, "/databases/{id}/tls", `{"enabled":true}`, auth.RoleReadOnly, http.StatusForbidden},
		{http.MethodPut, "/databases/{id}/tls", `{"enabled":true}`, auth.RoleLimited, http.StatusForbidden},
		{http.MethodPost, "/databases/{id}/tls/renew", "", auth.RoleLimited, http.StatusForbidden},
		{http.MethodGet, "/databases/tls/authority", "", auth.RoleReadOnly, http.StatusNotFound},
	} {
		_, rec, _ := tlsRoute(t, c.role, c.method, c.path, c.body)
		if rec.Code != c.want {
			t.Errorf("%s %s as %s answered %d %s, want %d", c.method, c.path, c.role, rec.Code, strings.TrimSpace(rec.Body.String()), c.want)
		}
	}
}

// A connection that is not a container of ours is told why, in the status and
// in the refusal of a change, and nothing is recorded for a change not made.
func TestTLSOfAServerThatIsNotOursIsExplained(t *testing.T) {
	_, rec, _ := tlsRoute(t, auth.RoleAdmin, http.MethodGet, "/databases/{id}/tls", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	var status dbTLSStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Supported || status.Reason == "" || status.Enabled || status.Names == nil || status.Authority != nil {
		t.Errorf("status = %+v", status)
	}
	s, rec, _ := tlsRoute(t, auth.RoleAdmin, http.MethodPut, "/databases/{id}/tls", `{"enabled":true,"require":true}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "publishes") {
		t.Errorf("a change was answered %d %s", rec.Code, rec.Body.String())
	}
	var made int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_tls_ca`).Scan(&made); err != nil || made != 0 {
		t.Errorf("an authority was made for a server that cannot use one: %d %v", made, err)
	}
}

func TestAuthorityDownloadIsTheCertificateAlone(t *testing.T) {
	s, h, _ := redisRouter(t, auth.RoleReadOnly, nowhere)
	if _, _, _, err := s.loadDBTLSAuthority(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/databases/tls/authority", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download = %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "-----BEGIN CERTIFICATE-----") || strings.Contains(body, "PRIVATE KEY") {
		t.Errorf("download is not the bare certificate: %.80s", body)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "just-dashboard-database-ca.pem") {
		t.Errorf("Content-Disposition = %q", got)
	}
}
