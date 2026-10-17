package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbtls"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/docker/docker/errdefs"
)

// TLS for the databases this dashboard started.
//
// The dashboard keeps one certificate authority of its own, made the first time
// it is needed, with its key sealed under the master key like every other secret
// at rest. A database it started can be given a server certificate from it: the
// certificate and key go into a volume of that container, the engine is told to
// use them, and the connection the dashboard keeps to it is rewritten to verify
// the certificate against the authority. The server stays where it was — on
// loopback, on the deployments' network, or published through the exposure
// switch — and the certificate names the addresses it is reached at.
//
// Turning it on, off, or toward refusing plaintext recreates the container, as
// the exposure switch does, and is put back as it was if the server does not come
// up speaking what it was told to. Renewing a certificate does not: the new
// files are written under the running server, which is asked to read them again,
// and a restart is the fallback only where it declines.

// dbTLSReadyWait is how long a recreated server is given to answer.
const dbTLSReadyWait = 90 * time.Second

// dbTLSState is what the Server holds for this: one change at a time, whether
// it is a person's or the renewal's, and the renewal's worker.
type dbTLSState struct {
	mu   sync.Mutex
	stop context.CancelFunc
	done chan struct{}
}

// --- the authority ----------------------------------------------------------

// dbTLSAuthority is the authority as stored: public parts only.
type dbTLSAuthority struct {
	CertPEM     []byte
	Fingerprint string
	Subject     string
	NotAfter    time.Time
	CreatedAt   time.Time
}

// readDBTLSAuthority reads the authority's certificate, or nil when none has
// been made. The key is not read.
func (s *Server) readDBTLSAuthority(ctx context.Context) (*dbTLSAuthority, error) {
	var (
		a               dbTLSAuthority
		notAfter, built int64
	)
	err := s.Store.DB.QueryRowContext(ctx,
		`SELECT cert_pem, fingerprint, not_after, created_at FROM db_tls_ca WHERE id = 1`).
		Scan(&a.CertPEM, &a.Fingerprint, &notAfter, &built)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, httpx.Internal(err)
	}
	info, err := dbtls.Inspect(a.CertPEM)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	a.Subject, a.NotAfter, a.CreatedAt = info.Subject, time.Unix(notAfter, 0).UTC(), time.Unix(built, 0).UTC()
	return &a, nil
}

// loadDBTLSAuthority opens the authority to sign with, making it when there is
// none and create is set. The caller holds dbTLS.mu.
func (s *Server) loadDBTLSAuthority(ctx context.Context, create bool) (*dbtls.CA, *dbTLSAuthority, bool, error) {
	var keyEnc string
	existing, err := s.readDBTLSAuthority(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	made := false
	if existing == nil {
		if !create {
			return nil, nil, false, httpx.Err(http.StatusNotFound, "no_authority",
				"no certificate authority has been made yet; switching TLS on for a database makes it")
		}
		now := time.Now()
		certPEM, keyPEM, err := dbtls.NewCA(now)
		if err != nil {
			return nil, nil, false, httpx.Internal(err)
		}
		sealed, err := s.Sealer.Seal(string(keyPEM))
		if err != nil {
			return nil, nil, false, httpx.Internal(err)
		}
		info, err := dbtls.Inspect(certPEM)
		if err != nil {
			return nil, nil, false, httpx.Internal(err)
		}
		if _, err := s.Store.DB.ExecContext(ctx,
			`INSERT INTO db_tls_ca (id, cert_pem, key_enc, fingerprint, not_after, created_at) VALUES (1, ?, ?, ?, ?, ?)`,
			string(certPEM), sealed, info.Fingerprint, info.NotAfter.Unix(), now.Unix()); err != nil {
			return nil, nil, false, httpx.Internal(err)
		}
		if existing, err = s.readDBTLSAuthority(ctx); err != nil || existing == nil {
			return nil, nil, false, httpx.Internal(errors.New("the certificate authority could not be read back"))
		}
		made = true
	}
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT key_enc FROM db_tls_ca WHERE id = 1`).Scan(&keyEnc); err != nil {
		return nil, nil, false, httpx.Internal(err)
	}
	keyPEM, err := s.Sealer.Open(keyEnc)
	if err != nil {
		return nil, nil, false, httpx.Err(http.StatusConflict, "authority_sealed",
			"the certificate authority's key could not be opened; JD_MASTER_KEY is not the key it was sealed under")
	}
	ca, err := dbtls.LoadCA(existing.CertPEM, []byte(keyPEM))
	if err != nil {
		return nil, nil, false, httpx.Internal(err)
	}
	return ca, existing, made, nil
}

// dbTLSCAFile is where the authority's certificate lies on this machine, the
// file the dashboard's saved connections verify against. It is written when it
// is missing or differs, so a data directory restored without it is mended by
// the next start.
func (s *Server) dbTLSCAFile(certPEM []byte) (string, error) {
	dir := filepath.Join(s.Cfg.DataDir, "tls")
	file := filepath.Join(dir, "ca.pem")
	if current, err := os.ReadFile(file); err == nil && bytes.Equal(current, certPEM) {
		return file, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, certPEM, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, file); err != nil {
		return "", err
	}
	return file, nil
}

// --- what a connection has ---------------------------------------------------

// dbTLSRow is a database server's certificate as recorded.
type dbTLSRow struct {
	Container string
	Require   bool
	Names     []string
	Serial    string
	IssuedAt  time.Time
	NotAfter  time.Time
	RenewedAt time.Time
}

func (s *Server) dbTLSRow(ctx context.Context, id int64) (*dbTLSRow, error) {
	var (
		row                     dbTLSRow
		require                 int
		names                   string
		issued, notAfter, renew int64
	)
	err := s.Store.DB.QueryRowContext(ctx,
		`SELECT container, require_tls, names, serial, issued_at, not_after, renewed_at FROM db_server_tls WHERE connection_id = ?`, id).
		Scan(&row.Container, &require, &names, &row.Serial, &issued, &notAfter, &renew)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, httpx.Internal(err)
	}
	_ = json.Unmarshal([]byte(names), &row.Names)
	row.Require = require != 0
	row.IssuedAt, row.NotAfter = time.Unix(issued, 0).UTC(), time.Unix(notAfter, 0).UTC()
	if renew > 0 {
		row.RenewedAt = time.Unix(renew, 0).UTC()
	}
	return &row, nil
}

func (s *Server) saveDBTLSRow(ctx context.Context, id int64, row *dbTLSRow) error {
	names, _ := json.Marshal(row.Names)
	var renewed int64
	if !row.RenewedAt.IsZero() {
		renewed = row.RenewedAt.Unix()
	}
	_, err := s.Store.DB.ExecContext(ctx,
		`INSERT INTO db_server_tls (connection_id, container, require_tls, names, serial, issued_at, not_after, renewed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(connection_id) DO UPDATE SET container = excluded.container, require_tls = excluded.require_tls,
		   names = excluded.names, serial = excluded.serial, issued_at = excluded.issued_at,
		   not_after = excluded.not_after, renewed_at = excluded.renewed_at`,
		id, row.Container, boolInt(row.Require), string(names), row.Serial, row.IssuedAt.Unix(), row.NotAfter.Unix(), renewed)
	if err != nil {
		return httpx.Internal(err)
	}
	return nil
}

// saveDBConnectionDSN replaces a connection's sealed string and drops what was
// dialled with the old one.
func (s *Server) saveDBConnectionDSN(ctx context.Context, id int64, dsn string) error {
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		return httpx.Internal(err)
	}
	if _, err := s.Store.DB.ExecContext(ctx, `UPDATE db_connections SET dsn_enc = ? WHERE id = ?`, sealed, id); err != nil {
		return httpx.Internal(err)
	}
	s.modules.dbs.Close(id)
	s.dbConns.stale(id)
	return nil
}

// --- the server behind a connection ------------------------------------------

type dbTLSTarget struct {
	conn   *dbConnection
	dsn    string
	info   *dbx.ConnInfo
	server *dbServer
	key    string
	tmpl   provisionTemplate
	ver    provisionVersion
	engine dbTLSEngine
}

// provisionTemplateOf finds the template an image was started from.
func provisionTemplateOf(image string) (string, provisionTemplate, provisionVersion, bool) {
	want := dockerx.ImageRef(image)
	for _, key := range provisionOrder {
		tmpl := provisionTemplates[key]
		for _, v := range tmpl.versions {
			if dockerx.ImageRef(v.image) == want {
				return key, tmpl, v, true
			}
		}
	}
	return "", provisionTemplate{}, provisionVersion{}, false
}

// resolveDBTLS finds the container a connection's TLS acts on. A reason, rather
// than an error, says why there is none: the status shows it, and a change is
// refused with it.
func (s *Server) resolveDBTLS(ctx context.Context, id int64) (*dbTLSTarget, string, error) {
	conn, dsn, err := s.dbConnRow(ctx, id)
	if err != nil {
		return nil, "", err
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil || conn.Driver == dbx.DriverSQLite || !databaseLoopback(info.Host) {
		return nil, "The server is not a container this dashboard reaches over loopback, so its certificate is not the dashboard's to manage.", nil
	}
	if s.modules.docker == nil {
		return nil, "This host has no Docker socket.", nil
	}
	server := s.serverBehind(ctx, conn, info)
	if server == nil || server.binding == nil {
		return nil, "No running container publishes this connection's port. Start the server to manage its TLS.", nil
	}
	if project := server.container.Labels["com.docker.compose.project"]; project != "" {
		return nil, fmt.Sprintf("%s belongs to the compose project %s. Set up TLS in its compose file.", server.container.Name, project), nil
	}
	key, tmpl, ver, ok := provisionTemplateOf(server.container.Image)
	engine, offered := dbTLSEngines[key]
	if !ok || !offered {
		return nil, fmt.Sprintf("%s was not started from one of the dashboard's TLS-capable templates, so the dashboard does not know how to switch it to TLS.", server.container.Name), nil
	}
	return &dbTLSTarget{conn: conn, dsn: dsn, info: info, server: server, key: key, tmpl: tmpl, ver: ver, engine: engine}, "", nil
}

// dbTLSNames are the names and addresses a client reaches the server by:
// loopback, the deployments' network name, the container's own name, and the
// address it is published on when that is not loopback.
func dbTLSNames(t *dbTLSTarget) []string {
	names := []string{"127.0.0.1", "::1", "localhost", databaseDNSName(t.conn.ID)}
	if name := strings.ToLower(t.server.container.Name); dbtls.IsHostName(name) {
		names = append(names, name)
	}
	switch exposureOf(t.server) {
	case exposurePublic:
		names = append(names, proxysvc.PublicAddresses()...)
	case exposurePrivate:
		names = append(names, strings.Trim(t.server.binding.IP, "[]"))
	}
	return names
}

// --- status ------------------------------------------------------------------

type dbTLSStatus struct {
	// Supported says whether the dashboard can manage this server's TLS; when it
	// cannot, Reason says why.
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	Enabled   bool   `json:"enabled"`
	// Require reports that a connection without TLS is refused.
	Require bool `json:"require"`
	// CanOptional is false for an engine whose port speaks one protocol, where
	// TLS on means plaintext off.
	CanOptional bool `json:"canOptional"`
	// Verified reports that the dashboard's own saved connection checks the
	// certificate against the authority.
	Verified  bool       `json:"verified"`
	Container string     `json:"container,omitempty"`
	Port      int        `json:"port,omitempty"`
	Exposure  dbExposure `json:"exposure,omitempty"`
	Names     []string   `json:"names"`
	Serial    string     `json:"serial,omitempty"`
	IssuedAt  *time.Time `json:"issuedAt,omitempty"`
	NotAfter  *time.Time `json:"notAfter,omitempty"`
	// RenewAt is when the renewal will take it up; RenewedAt is when it last did.
	RenewAt   *time.Time `json:"renewAt,omitempty"`
	RenewedAt *time.Time `json:"renewedAt,omitempty"`
	// Applications says what a linked application is given: "plain" (the string
	// it had before TLS, while TLS is only allowed), "unverified" (TLS without
	// checking the certificate, once TLS is required) or "needsCA" (TLS that
	// checks, which an application with no copy of the authority cannot do).
	Applications string `json:"applications,omitempty"`
	// Linked counts the deployment environments that reach this database by its
	// internal name, which stop connecting if they do not speak TLS once it is
	// required.
	Linked    int                `json:"linked"`
	Authority *dbTLSAuthorityOut `json:"authority"`
	Client    *dbTLSClient       `json:"client,omitempty"`
}

type dbTLSAuthorityOut struct {
	Subject     string    `json:"subject"`
	Fingerprint string    `json:"fingerprint"`
	NotAfter    time.Time `json:"notAfter"`
	CreatedAt   time.Time `json:"createdAt"`
}

// dbTLSClient is what somebody on another machine types to connect with the
// certificate checked. The password is a placeholder: it is never returned.
type dbTLSClient struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	URL     string `json:"url,omitempty"`
	Command string `json:"command"`
	CAFile  string `json:"caFile"`
}

func (s *Server) handleDBTLS(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	status, err := s.describeDBTLS(ctx, id)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

func (s *Server) describeDBTLS(ctx context.Context, id int64) (*dbTLSStatus, error) {
	conn, _, err := s.dbConnRow(ctx, id)
	if err != nil {
		return nil, err
	}
	t, reason, err := s.resolveDBTLS(ctx, id)
	if err != nil {
		return nil, err
	}
	row, err := s.dbTLSRow(ctx, id)
	if err != nil {
		return nil, err
	}
	authority, err := s.readDBTLSAuthority(ctx)
	if err != nil {
		return nil, err
	}
	out := &dbTLSStatus{Supported: t != nil, Reason: reason, Names: []string{}}
	if authority != nil {
		out.Authority = &dbTLSAuthorityOut{
			Subject: authority.Subject, Fingerprint: authority.Fingerprint,
			NotAfter: authority.NotAfter, CreatedAt: authority.CreatedAt,
		}
	}
	if row != nil {
		out.Enabled, out.Require, out.Container = true, row.Require, row.Container
		out.Names, out.Serial = row.Names, row.Serial
		issued, notAfter, renewAt := row.IssuedAt, row.NotAfter, row.NotAfter.Add(-dbtls.RenewWindow)
		out.IssuedAt, out.NotAfter, out.RenewAt = &issued, &notAfter, &renewAt
		if !row.RenewedAt.IsZero() {
			renewed := row.RenewedAt
			out.RenewedAt = &renewed
		}
		out.Applications = applicationsGet(row.Require, conn.Driver)
	}
	var linked int
	if err := s.Store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM deploy_database_bindings WHERE connection_id = ?`, id).Scan(&linked); err != nil {
		return nil, httpx.Internal(err)
	}
	out.Linked = linked
	if t == nil {
		return out, nil
	}
	out.Container = t.server.container.Name
	out.Port, _ = strconv.Atoi(t.info.Port)
	out.Exposure = exposureOf(t.server)
	out.CanOptional = t.engine.optional
	out.Verified = dbx.ServerTLSRequired(t.conn.Driver, t.dsn)
	if out.Enabled && !t.engine.optional {
		out.Require = true
	}
	if out.Enabled {
		out.Client = dbTLSClientFor(t, out.Exposure)
	}
	return out, nil
}

// refuseWhileTLS stops a connection being forgotten while its server still
// speaks TLS with a certificate this dashboard renews: the record of it goes
// with the connection, the server keeps serving the certificate, nothing renews
// it, and connecting to the server again would dial plain text.
func (s *Server) refuseWhileTLS(ctx context.Context, id int64) error {
	row, err := s.dbTLSRow(ctx, id)
	if err != nil {
		return err
	}
	if row != nil {
		return httpx.Err(http.StatusConflict, "tls_on",
			"this database's server speaks TLS with a certificate this dashboard renews; turn TLS off in its Network settings before forgetting the connection")
	}
	return nil
}

// applicationsGet names what a linked application is given: see
// dbTLSStatus.Applications. Redis has no setting to skip the check in a URL.
func applicationsGet(require bool, driver dbx.Driver) string {
	switch {
	case !require:
		return "plain"
	case driver == dbx.DriverRedis:
		return "needsCA"
	}
	return "unverified"
}

// applicationDSN is a connection's string for a program in another container,
// or on the host's network: the saved one with the dashboard's own file of the
// authority taken out (dbx.ForApplication). Whether TLS is required is the
// server's, read from the record.
func (s *Server) applicationDSN(ctx context.Context, conn *dbConnection, dsn string) string {
	if dbx.ServerTLSFile(conn.Driver, dsn) == "" {
		return dsn
	}
	row, err := s.dbTLSRow(ctx, conn.ID)
	required := err != nil || row == nil || row.Require
	return dbx.ForApplication(conn.Driver, dsn, required)
}

// dbTLSClientFor words the connection for a program on another machine.
func dbTLSClientFor(t *dbTLSTarget, exposure dbExposure) *dbTLSClient {
	host := "127.0.0.1"
	switch exposure {
	case exposurePublic:
		if addr := preferredPublicAddress(proxysvc.PublicAddresses()); addr != "" {
			host = addr
		}
	case exposurePrivate:
		host = strings.Trim(t.server.binding.IP, "[]")
	}
	port, _ := strconv.Atoi(t.info.Port)
	hostPort := net.JoinHostPort(host, strconv.Itoa(port))
	client := &dbTLSClient{Host: host, Port: port, CAFile: "ca.pem"}
	user, database := t.info.User, t.info.Database
	switch t.conn.Driver {
	case dbx.DriverPostgres:
		client.URL = fmt.Sprintf("postgres://%s:<password>@%s/%s?sslmode=verify-full&sslrootcert=ca.pem", url.PathEscape(user), hostPort, url.PathEscape(database))
		client.Command = fmt.Sprintf("psql %q", client.URL)
	case dbx.DriverMySQL:
		if t.key == "mariadb" {
			client.Command = fmt.Sprintf("mariadb --host=%s --port=%d --user=%s --password --ssl-verify-server-cert --ssl-ca=ca.pem %s", host, port, user, database)
		} else {
			client.Command = fmt.Sprintf("mysql --host=%s --port=%d --user=%s --password --ssl-mode=VERIFY_IDENTITY --ssl-ca=ca.pem %s", host, port, user, database)
		}
	case dbx.DriverMongo:
		query := url.Values{}
		if parsed, err := url.Parse(t.dsn); err == nil {
			if source := parsed.Query().Get("authSource"); source != "" {
				query.Set("authSource", source)
			}
		}
		query.Set("tls", "true")
		query.Set("tlsCAFile", "ca.pem")
		client.URL = fmt.Sprintf("mongodb://%s:<password>@%s/%s?%s", url.PathEscape(user), hostPort, url.PathEscape(database), query.Encode())
		client.Command = fmt.Sprintf("mongosh %q", client.URL)
	case dbx.DriverRedis:
		cli := "redis-cli"
		if t.key == "valkey" {
			cli = "valkey-cli"
		}
		client.URL = fmt.Sprintf("rediss://:<password>@%s/0", hostPort)
		client.Command = fmt.Sprintf("%s --tls --cacert ca.pem -h %s -p %d -a '<password>'", cli, host, port)
	}
	return client
}

func (s *Server) handleDBTLSAuthority(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	authority, err := s.readDBTLSAuthority(r.Context())
	if err != nil {
		return err
	}
	if authority == nil {
		return httpx.Err(http.StatusNotFound, "no_authority",
			"no certificate authority has been made yet; switching TLS on for a database makes it")
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="just-dashboard-database-ca.pem"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(authority.CertPEM)
	return nil
}

// --- changing it ---------------------------------------------------------------

type dbTLSRequest struct {
	Enabled bool `json:"enabled"`
	Require bool `json:"require"`
}

func (s *Server) handleDBTLSUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req dbTLSRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 8*time.Minute)
	defer cancel()
	name, detail, err := s.setDBTLS(ctx, id, req.Enabled, req.Require)
	if name != "" {
		httpx.SetAudit(r, "database.tls.change", name, detail)
	}
	if err != nil {
		return err
	}
	status, err := s.describeDBTLS(ctx, id)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

// dbTLSVolume is the volume that holds a container's certificate.
func dbTLSVolume(container string) string { return container + dbTLSVolumeSuffix }

func hasDBTLSMount(spec *dockerx.ContainerSpec) bool { return mountSource(spec) != "" }

// dbTLSVolumeLabel marks a volume this dashboard made for a connection's
// certificate, with the connection's id. It is what lets a volume of the same
// name that somebody else made be left alone.
const dbTLSVolumeLabel = "jd.tls-of"

// mountSource is the volume a spec mounts the certificate from, or empty.
func mountSource(spec *dockerx.ContainerSpec) string {
	for _, m := range spec.Mounts {
		if m.Target == dbTLSDir {
			return m.Source
		}
	}
	return ""
}

// claimDBTLSVolume returns the volume a connection's certificate goes in,
// making it with the label when it is not there. A volume of that name that
// carries no such label, or another connection's, is somebody else's data and is
// refused rather than mounted or, later, removed.
func (s *Server) claimDBTLSVolume(ctx context.Context, container string, id int64) (string, bool, error) {
	name := dbTLSVolume(container)
	owner := strconv.FormatInt(id, 10)
	existing, err := s.modules.docker.InspectVolume(ctx, name)
	switch {
	case err == nil:
		if existing.Labels[dbTLSVolumeLabel] != owner {
			return "", false, httpx.Err(http.StatusConflict, "volume_exists", fmt.Sprintf(
				"a volume named %s already exists and was not made by this dashboard for this database; rename or remove it on the Docker page first", name))
		}
		return name, false, nil
	case !errdefs.IsNotFound(err):
		return "", false, s.dockerErr(err)
	}
	if _, err := s.modules.docker.CreateVolume(ctx, dockerx.VolumeSpec{Name: name, Labels: map[string]string{dbTLSVolumeLabel: owner}}); err != nil {
		return "", false, s.dockerErr(err)
	}
	return name, true, nil
}

// dropDBTLSVolume removes a certificate volume this dashboard made for the
// connection. One that is not there, is somebody else's, or is still held is
// left.
func (s *Server) dropDBTLSVolume(ctx context.Context, name string, id int64) {
	if name == "" {
		return
	}
	rescue, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	existing, err := s.modules.docker.InspectVolume(rescue, name)
	if err != nil || existing.Labels[dbTLSVolumeLabel] != strconv.FormatInt(id, 10) {
		return
	}
	_ = s.modules.docker.RemoveVolume(rescue, name, false)
}

// writeDBTLSFiles is the BeforeStart that puts the certificate where the server
// reads it.
func (s *Server) writeDBTLSFiles(files []dbTLSFile) dockerx.BeforeStart {
	return func(ctx context.Context, id string) error {
		for _, f := range files {
			if err := s.modules.docker.WriteContainerFile(ctx, id, dbTLSDir+"/"+f.name, f.content, f.mode); err != nil {
				return fmt.Errorf("could not write %s: %w", f.name, err)
			}
		}
		return nil
	}
}

// waitDBTLSReady dials until the server answers with the connection string it
// is expected to be reached by, or the wait runs out; the last refusal is
// returned.
func (s *Server) waitDBTLSReady(ctx context.Context, driver dbx.Driver, dsn string) error {
	deadline := time.Now().Add(dbTLSReadyWait)
	for {
		err := s.dialDatabase(ctx, driver, dsn)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(2 * time.Second):
		}
	}
}

// setDBTLS switches a connection's server to TLS, changes whether plaintext is
// refused, or switches TLS off. It returns the connection's name and the audit
// detail even when it fails, so that a refusal that cost a restart is on record.
func (s *Server) setDBTLS(ctx context.Context, id int64, enabled, require bool) (string, map[string]any, error) {
	s.dbTLS.mu.Lock()
	defer s.dbTLS.mu.Unlock()

	t, reason, err := s.resolveDBTLS(ctx, id)
	if err != nil {
		return "", nil, err
	}
	if reason != "" {
		return "", nil, httpx.BadRequest("%s", reason)
	}
	name := t.conn.Name
	detail := map[string]any{"container": t.server.container.Name, "enabled": enabled}

	spec, err := s.modules.docker.SpecOf(ctx, t.server.container.ID)
	if err != nil {
		return name, detail, s.dockerErr(err)
	}
	before, err := s.modules.docker.SpecOf(ctx, t.server.container.ID)
	if err != nil {
		return name, detail, s.dockerErr(err)
	}
	original, on, err := t.engine.readCommand(t.tmpl, spec)
	if err != nil {
		return name, detail, httpx.BadRequest("%s cannot be switched: %v", t.server.container.Name, err)
	}
	row, err := s.dbTLSRow(ctx, id)
	if err != nil {
		return name, detail, err
	}
	if !enabled {
		return name, detail, s.removeDBTLS(ctx, t, spec, before, original, on, row, detail)
	}
	if !t.engine.optional {
		require = true
	}
	detail["require"] = require
	if on && row != nil && row.Require == require && dbx.ServerTLSRequired(t.conn.Driver, t.dsn) {
		detail["unchanged"] = true
		return name, detail, nil
	}
	return name, detail, s.addDBTLS(ctx, t, spec, before, original, require, row, detail)
}

func (s *Server) addDBTLS(ctx context.Context, t *dbTLSTarget, spec, before *dockerx.ContainerSpec, original []string,
	require bool, row *dbTLSRow, detail map[string]any) error {
	ca, authority, made, err := s.loadDBTLSAuthority(ctx, true)
	if err != nil {
		return err
	}
	caFile, err := s.dbTLSCAFile(authority.CertPEM)
	if err != nil {
		return httpx.Internal(fmt.Errorf("the authority's certificate could not be written: %w", err))
	}
	leaf, err := ca.Issue(dbTLSNames(t), time.Now())
	if err != nil {
		return httpx.BadRequest("a certificate could not be made: %v", err)
	}
	newDSN, err := dbx.WithServerTLS(t.conn.Driver, t.dsn, caFile)
	if err != nil {
		return httpx.BadRequest("this connection's string cannot carry TLS settings: %v", err)
	}
	volume, created := mountSource(spec), false
	if volume == "" {
		if volume, created, err = s.claimDBTLSVolume(ctx, t.server.container.Name, t.conn.ID); err != nil {
			return err
		}
		spec.Mounts = append(spec.Mounts, dockerx.MountSpec{Type: "volume", Source: volume, Target: dbTLSDir})
	}
	spec.Command = t.engine.command(original, require)
	files := append(t.engine.files(leaf, authority.CertPEM), t.engine.extraFiles(require)...)

	res, err := s.modules.docker.Recreate(ctx, t.server.container.ID,
		dockerx.RecreateOptions{Spec: spec, BeforeStart: s.writeDBTLSFiles(files)}, nil)
	if err != nil {
		if created {
			s.dropDBTLSVolume(ctx, volume, t.conn.ID)
		}
		return s.dockerErr(err)
	}
	s.modules.dbs.Close(t.conn.ID)
	s.dbConns.stale(t.conn.ID)
	if err := s.waitDBTLSReady(ctx, t.conn.Driver, newDSN); err != nil {
		drop := ""
		if created {
			drop = volume
		}
		back := s.putBackDBTLS(ctx, res.ID, before, drop, t.conn.ID, detail)
		return httpx.Err(http.StatusBadGateway, "tls_failed", fmt.Sprintf(
			"%s did not accept connections with TLS on (%s). %s", t.server.container.Name, connectError(newDSN, err), putBackWords(t.server.container.Name, back)))
	}
	if err := s.saveDBConnectionDSN(ctx, t.conn.ID, newDSN); err != nil {
		return err
	}
	now := time.Now()
	next := &dbTLSRow{Container: t.server.container.Name, Require: require, Names: leaf.Names, Serial: leaf.Serial, IssuedAt: now, NotAfter: leaf.NotAfter}
	if row != nil {
		next.RenewedAt = row.RenewedAt
	}
	if err := s.saveDBTLSRow(ctx, t.conn.ID, next); err != nil {
		return err
	}
	detail["names"], detail["serial"], detail["notAfter"] = leaf.Names, leaf.Serial, leaf.NotAfter
	detail["authority"], detail["authorityMade"] = authority.Fingerprint, made
	if row != nil {
		detail["from"] = map[string]any{"require": row.Require}
	}
	if len(res.Warnings) > 0 {
		detail["warnings"] = res.Warnings
	}
	return nil
}

func (s *Server) removeDBTLS(ctx context.Context, t *dbTLSTarget, spec, before *dockerx.ContainerSpec, original []string,
	on bool, row *dbTLSRow, detail map[string]any) error {
	volume := mountSource(spec)
	if !on && row == nil && volume == "" {
		detail["unchanged"] = true
		return nil
	}
	newDSN, err := dbx.WithoutServerTLS(t.conn.Driver, t.dsn)
	if err != nil {
		return httpx.BadRequest("this connection's string cannot be read: %v", err)
	}
	mounts := spec.Mounts[:0]
	for _, m := range spec.Mounts {
		if m.Target != dbTLSDir {
			mounts = append(mounts, m)
		}
	}
	spec.Mounts = mounts
	if on {
		spec.Command = t.engine.plain(t.tmpl, original)
	}
	res, err := s.modules.docker.Recreate(ctx, t.server.container.ID, dockerx.RecreateOptions{Spec: spec}, nil)
	if err != nil {
		return s.dockerErr(err)
	}
	s.modules.dbs.Close(t.conn.ID)
	s.dbConns.stale(t.conn.ID)
	if err := s.waitDBTLSReady(ctx, t.conn.Driver, newDSN); err != nil {
		// The files it had are still in the volume, which is what lets the
		// server start the way it did.
		back := s.putBackDBTLS(ctx, res.ID, before, "", t.conn.ID, detail)
		return httpx.Err(http.StatusBadGateway, "tls_failed", fmt.Sprintf(
			"%s did not accept connections with TLS off (%s). %s", t.server.container.Name, connectError(newDSN, err), putBackWords(t.server.container.Name, back)))
	}
	if err := s.saveDBConnectionDSN(ctx, t.conn.ID, newDSN); err != nil {
		return err
	}
	if _, err := s.Store.DB.ExecContext(ctx, `DELETE FROM db_server_tls WHERE connection_id = ?`, t.conn.ID); err != nil {
		return httpx.Internal(err)
	}
	s.dropDBTLSVolume(ctx, volume, t.conn.ID)
	if len(res.Warnings) > 0 {
		detail["warnings"] = res.Warnings
	}
	return nil
}

// putBackDBTLS returns a container to the configuration it had before a change
// that did not take, and removes the volume the change made, if it made one. It
// runs on a context that outlives the request's, because the request's may be
// what ran out. Its error is what the response has to say, since a database
// that could not be put back may not be reachable.
func (s *Server) putBackDBTLS(ctx context.Context, id string, before *dockerx.ContainerSpec, drop string, connID int64, detail map[string]any) error {
	rescue, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer cancel()
	if _, err := s.modules.docker.Recreate(rescue, id, dockerx.RecreateOptions{Spec: before}, nil); err != nil {
		detail["putBackError"] = err.Error()
		return err
	}
	detail["putBack"] = true
	s.dropDBTLSVolume(rescue, drop, connID)
	return nil
}

// putBackWords says what became of the container after a change that did not take.
func putBackWords(container string, err error) string {
	if err == nil {
		return "It was put back as it was."
	}
	return fmt.Sprintf("Putting it back failed too (%v), so the database may be unreachable. Look at %s on the Docker page: if the earlier container is parked there under a name ending _jd_replaced_…, rename it back and start it.", err, container)
}

// --- renewing --------------------------------------------------------------------

func (s *Server) handleDBTLSRenew(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()
	name, detail, err := s.renewDBTLS(ctx, id, "requested")
	if name != "" {
		httpx.SetAudit(r, "database.tls.renew", name, detail)
	}
	if err != nil {
		return err
	}
	status, err := s.describeDBTLS(ctx, id)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

// renewDBTLS issues a new certificate for a server that has one and has the
// running server read it. The reason is "requested", "expiry" or "names".
func (s *Server) renewDBTLS(ctx context.Context, id int64, reason string) (string, map[string]any, error) {
	s.dbTLS.mu.Lock()
	defer s.dbTLS.mu.Unlock()
	return s.renewDBTLSLocked(ctx, id, reason)
}

// renewDBTLSLocked is renewDBTLS for a caller that holds dbTLS.mu.
func (s *Server) renewDBTLSLocked(ctx context.Context, id int64, reason string) (string, map[string]any, error) {
	row, err := s.dbTLSRow(ctx, id)
	if err != nil {
		return "", nil, err
	}
	if row == nil {
		return "", nil, httpx.BadRequest("TLS is not switched on for this database")
	}
	t, why, err := s.resolveDBTLS(ctx, id)
	if err != nil {
		return "", nil, err
	}
	if why != "" {
		return "", nil, httpx.BadRequest("%s", why)
	}
	name := t.conn.Name
	detail := map[string]any{"container": t.server.container.Name, "reason": reason, "previousSerial": row.Serial, "previousNotAfter": row.NotAfter}
	ca, authority, _, err := s.loadDBTLSAuthority(ctx, false)
	if err != nil {
		return name, detail, err
	}
	caFile, err := s.dbTLSCAFile(authority.CertPEM)
	if err != nil {
		return name, detail, httpx.Internal(err)
	}
	leaf, err := ca.Issue(dbTLSNames(t), time.Now())
	if err != nil {
		return name, detail, httpx.BadRequest("a certificate could not be made: %v", err)
	}
	container := t.server.container.ID
	files := t.engine.files(leaf, authority.CertPEM)
	// Written beside the pair the server reads and swapped in by one command,
	// so a failure part way leaves the old pair, not a certificate with another
	// certificate's key.
	discard := func() {
		_, _, _ = s.modules.docker.ExecCheck(context.WithoutCancel(ctx), container, []string{"sh", "-c", t.engine.discardScript(files)}, 20*time.Second)
	}
	for _, f := range files {
		if err := s.modules.docker.WriteContainerFile(ctx, container, dbTLSDir+"/"+f.name+newSuffix, f.content, f.mode); err != nil {
			discard()
			return name, detail, s.dockerErr(err)
		}
	}
	if _, out, err := s.modules.docker.ExecCheck(ctx, container, []string{"sh", "-c", t.engine.swapScript(files)}, 20*time.Second); err != nil {
		discard()
		return name, detail, httpx.Err(http.StatusBadGateway, "tls_failed",
			fmt.Sprintf("the new certificate could not be put in place (%v %s); the old one is still in use", err, strings.TrimSpace(string(out))))
	}
	addr := net.JoinHostPort(t.info.Host, t.info.Port)
	reloadErr := s.reloadDBTLS(ctx, t, container)
	if reloadErr == nil {
		reloadErr = s.waitDBTLSServed(ctx, t, addr, caFile, leaf.Serial, 15*time.Second)
	}
	restarted := false
	if reloadErr != nil {
		// The engine would not read the new files while running, or said it
		// did and kept the old ones. They are in place, so a start reads them.
		detail["reloadError"] = reloadErr.Error()
		if err := s.modules.docker.Lifecycle(ctx, container, dockerx.ActionRestart, nil); err != nil {
			return name, detail, s.dockerErr(err)
		}
		restarted = true
		s.modules.dbs.Close(t.conn.ID)
		s.dbConns.stale(t.conn.ID)
	}
	detail["restarted"] = restarted
	if err := s.waitDBTLSReady(ctx, t.conn.Driver, t.dsn); err != nil {
		return name, detail, httpx.Err(http.StatusBadGateway, "tls_failed", fmt.Sprintf(
			"%s did not accept connections after its certificate was replaced (%s)", t.server.container.Name, connectError(t.dsn, err)))
	}
	if restarted {
		if err := s.waitDBTLSServed(ctx, t, addr, caFile, leaf.Serial, 30*time.Second); err != nil {
			return name, detail, httpx.Err(http.StatusBadGateway, "tls_failed", fmt.Sprintf(
				"%s is still presenting its old certificate after a restart (%v)", t.server.container.Name, err))
		}
	}
	next := &dbTLSRow{
		Container: t.server.container.Name, Require: row.Require, Names: leaf.Names, Serial: leaf.Serial,
		IssuedAt: time.Now(), NotAfter: leaf.NotAfter, RenewedAt: time.Now(),
	}
	if err := s.saveDBTLSRow(ctx, t.conn.ID, next); err != nil {
		return name, detail, err
	}
	detail["names"], detail["serial"], detail["notAfter"] = leaf.Names, leaf.Serial, leaf.NotAfter
	return name, detail, nil
}

// reloadDBTLS asks a running server to read the certificate files again.
func (s *Server) reloadDBTLS(ctx context.Context, t *dbTLSTarget, container string) error {
	_, out, err := s.modules.docker.ExecCheck(ctx, container, []string{"sh", "-c", t.engine.reload}, 40*time.Second)
	if err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
	}
	if t.engine.reloadReplies != nil && !t.engine.reloadReplies(string(out)) {
		return fmt.Errorf("the server did not accept the new files (%s)", strings.TrimSpace(string(out)))
	}
	return nil
}

// waitDBTLSServed waits for the server to present the certificate with this
// serial. A server asked to reload may take a moment, and may say it did and
// not have.
func (s *Server) waitDBTLSServed(ctx context.Context, t *dbTLSTarget, addr, caFile, serial string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		cert, err := dbx.ServedCertificate(ctx, t.conn.Driver, addr, caFile)
		switch {
		case err == nil && strings.EqualFold(cert.SerialNumber.Text(16), serial):
			return nil
		case err == nil:
			err = fmt.Errorf("it presents certificate %s", strings.ToUpper(cert.SerialNumber.Text(16)))
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Second):
		}
	}
}
