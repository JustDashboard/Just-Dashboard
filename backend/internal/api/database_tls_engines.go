package api

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbtls"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// How each database the dashboard starts is switched to TLS.
//
// The certificate and its key live in a volume of their own, mounted at
// dbTLSDir, so that they outlive the container being recreated and are never
// part of the data volume. The server is told to use them by the command it
// starts with. For the engines that take their settings as arguments the
// original command is kept, after a marker, and the TLS arguments follow it:
// an argument somebody added on the Docker page is still there, and switching
// TLS off puts the command back as it was. Redis reads a configuration file the
// start script writes, so there the script is replaced by the one that also
// carries the TLS lines. Both scripts are constant text: the only things
// interpolated into them are written in this file, and the one secret they read
// comes from the container's own environment.

const (
	// dbTLSDir is where the volume is mounted in the container.
	dbTLSDir = "/jd-tls"
	// dbTLSVolumeSuffix names the volume after the container it belongs to.
	dbTLSVolumeSuffix = "-tls"
	// dbTLSMarker is the script's $0. It is how a command this file made is told
	// from one somebody else wrote.
	dbTLSMarker = "jd-tls"
)

// dbTLSFile is one file in the volume.
type dbTLSFile struct {
	name    string
	content []byte
	mode    int64
}

type dbTLSEngine struct {
	// account owns the private key inside the image; the server drops to it
	// and cannot read a key that is root's.
	account string
	// servers are the programs the original command may start. A command that
	// starts anything else was written by somebody for a reason this file does
	// not know, and is not wrapped.
	servers []string
	// args are appended to the server's own command, for the engines that take
	// their TLS settings there. Nil for Redis, whose script writes them.
	args func(require bool) []string
	// redisServer is the program a Redis-protocol template starts.
	redisServer string
	// keyFile is the file in the volume that holds the private key.
	keyFile string
	// files are what a certificate is delivered as.
	files func(leaf *dbtls.Leaf, ca []byte) []dbTLSFile
	// reload makes a running server read the files again. It is shell text run
	// inside the container, where the account's password is already in the
	// environment the image was started with.
	reload string
	// reloadReplies, when set, says whether the reload's output means it took.
	reloadReplies func(out string) bool
	// hba is set for PostgreSQL, which refuses plaintext by a rule in a file
	// rather than by a setting.
	hba bool
	// optional reports an engine that can accept TLS and plaintext on the one
	// port. Redis cannot: a port speaks one protocol.
	optional bool
}

// certFiles delivers a certificate as a certificate and a key, or as the one
// file holding both that MongoDB reads.
func certFiles(keyName string) func(*dbtls.Leaf, []byte) []dbTLSFile {
	return func(leaf *dbtls.Leaf, ca []byte) []dbTLSFile {
		files := []dbTLSFile{{"ca.crt", ca, 0o644}}
		if keyName == "server.pem" {
			return append(files, dbTLSFile{"server.pem", leaf.CombinedPEM(), 0o600})
		}
		return append(files, dbTLSFile{"server.crt", leaf.CertPEM, 0o644}, dbTLSFile{keyName, leaf.KeyPEM, 0o600})
	}
}

var dbTLSEngines = map[string]dbTLSEngine{
	"postgres":      postgresTLS,
	"pgvector":      postgresTLS,
	"postgis":       postgresTLS,
	"postgres-full": postgresTLS,
	"timescaledb":   postgresTLS,
	"mysql": {
		account: "mysql", servers: []string{"mysqld"}, keyFile: "server.key", optional: true,
		args: func(require bool) []string {
			args := []string{"--ssl-ca=" + dbTLSDir + "/ca.crt", "--ssl-cert=" + dbTLSDir + "/server.crt", "--ssl-key=" + dbTLSDir + "/server.key"}
			if require {
				args = append(args, "--require-secure-transport=ON")
			}
			return args
		},
		files:  certFiles("server.key"),
		reload: `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e 'ALTER INSTANCE RELOAD TLS'`,
	},
	"mariadb": {
		account: "mysql", servers: []string{"mariadbd", "mysqld"}, keyFile: "server.key", optional: true,
		args: func(require bool) []string {
			args := []string{"--ssl-ca=" + dbTLSDir + "/ca.crt", "--ssl-cert=" + dbTLSDir + "/server.crt", "--ssl-key=" + dbTLSDir + "/server.key"}
			if require {
				args = append(args, "--require-secure-transport=ON")
			}
			return args
		},
		files:  certFiles("server.key"),
		reload: `mariadb -uroot -p"$MARIADB_ROOT_PASSWORD" -e 'FLUSH SSL'`,
	},
	"mongodb": {
		account: "mongodb", servers: []string{"mongod"}, keyFile: "server.pem", optional: true,
		args: func(require bool) []string {
			mode := "preferTLS"
			if require {
				mode = "requireTLS"
			}
			// Naming a CA makes mongod ask clients for a certificate of their
			// own unless told that a connection without one is fine.
			return []string{"--tlsMode", mode, "--tlsCertificateKeyFile", dbTLSDir + "/server.pem",
				"--tlsCAFile", dbTLSDir + "/ca.crt", "--tlsAllowConnectionsWithoutCertificates"}
		},
		files: certFiles("server.pem"),
		reload: `mongosh --quiet --tls --tlsCAFile ` + dbTLSDir + `/ca.crt --tlsAllowInvalidHostnames --host 127.0.0.1 ` +
			`-u "$MONGO_INITDB_ROOT_USERNAME" -p "$MONGO_INITDB_ROOT_PASSWORD" --authenticationDatabase admin ` +
			`--eval 'db.adminCommand({rotateCertificates: 1})'`,
	},
	"redis":  redisTLS("redis-server", "redis", "redis-cli"),
	"valkey": redisTLS("valkey-server", "valkey", "valkey-cli"),
}

var postgresTLS = dbTLSEngine{
	account: "postgres", servers: []string{"postgres"}, keyFile: "server.key", optional: true, hba: true,
	args: func(require bool) []string {
		args := []string{"-c", "ssl=on", "-c", "ssl_cert_file=" + dbTLSDir + "/server.crt", "-c", "ssl_key_file=" + dbTLSDir + "/server.key"}
		if require {
			args = append(args, "-c", "hba_file="+dbTLSDir+"/pg_hba.conf")
		}
		return args
	},
	files: certFiles("server.key"),
	// The server's own account is trusted over its socket, as the image sets it up.
	reload: `psql -U "$POSTGRES_USER" -d postgres -Atc 'select pg_reload_conf()'`,
}

// pgRequireHBA is pg_hba.conf with plaintext refused. PostgreSQL has no setting
// for that: a connection is turned away by a rule, so the file the image writes
// is reproduced here with one rule changed. The loopback rules inside the
// container stay as the image has them, for its own tools.
const pgRequireHBA = `local   all             all                                     trust
host    all             all             127.0.0.1/32            trust
host    all             all             ::1/128                 trust
local   replication     all                                     trust
host    replication     all             127.0.0.1/32            trust
host    replication     all             ::1/128                 trust
hostnossl all           all             all                     reject
hostssl all             all             all                     scram-sha-256
`

// redisTLS is a Redis-protocol server's profile. TLS replaces the plaintext
// port rather than sitting beside it, so the published port, the connection's
// address and the exposure switch all keep meaning what they did.
func redisTLS(server, account, cli string) dbTLSEngine {
	return dbTLSEngine{
		account: account, redisServer: server, keyFile: "server.key",
		files: certFiles("server.key"),
		reload: `REDISCLI_AUTH="$REDIS_PASSWORD" ` + cli + ` --tls --insecure -h 127.0.0.1 -p 6379 config set ` +
			`tls-cert-file ` + dbTLSDir + `/server.crt tls-key-file ` + dbTLSDir + `/server.key`,
		reloadReplies: func(out string) bool { return strings.Contains(out, "OK") },
	}
}

// redisTLSScript is the start script of a Redis-protocol server with TLS: the
// password configuration the provisioning script writes, and the TLS lines.
func redisTLSScript(server, account string) string {
	return `set -eu; umask 077; conf=/tmp/jd-` + server + `.conf; rm -f "$conf"; ` +
		`printf 'requirepass %s\nport 0\ntls-port 6379\ntls-cert-file ` + dbTLSDir + `/server.crt\n` +
		`tls-key-file ` + dbTLSDir + `/server.key\ntls-ca-cert-file ` + dbTLSDir + `/ca.crt\ntls-auth-clients no\n' ` +
		`"$REDIS_PASSWORD" > "$conf"; chown ` + account + `:` + account + ` "$conf" ` + dbTLSDir + `/server.key; ` +
		`exec docker-entrypoint.sh ` + server + ` "$conf"`
}

// wrapScript starts the image's entrypoint with the original command and the
// TLS arguments after it, once the key belongs to the account the server runs as.
func (e dbTLSEngine) wrapScript(require bool) string {
	return `set -eu; chown ` + e.account + `:` + e.account + ` ` + dbTLSDir + `/` + e.keyFile + `; ` +
		`chmod 0600 ` + dbTLSDir + `/` + e.keyFile + `; exec docker-entrypoint.sh "$@" ` + strings.Join(e.args(require), " ")
}

// newSuffix marks a file written beside the one it will replace.
const newSuffix = ".new"

// swapScript puts files written under their .new names in place of the ones
// the server reads, once the key belongs to the account the server runs as. It
// is one exec, so a renewal that fails while writing leaves the old pair
// untouched, and one that fails here has not run a second mv without the first.
func (e dbTLSEngine) swapScript(files []dbTLSFile) string {
	key := dbTLSDir + "/" + e.keyFile + newSuffix
	script := `set -e; chown ` + e.account + `:` + e.account + ` ` + key + `; chmod 0600 ` + key
	for _, f := range files {
		script += `; mv -f ` + dbTLSDir + `/` + f.name + newSuffix + ` ` + dbTLSDir + `/` + f.name
	}
	return script
}

// discardScript removes what a renewal wrote and did not put in place.
func (e dbTLSEngine) discardScript(files []dbTLSFile) string {
	script := `rm -f`
	for _, f := range files {
		script += ` ` + dbTLSDir + `/` + f.name + newSuffix
	}
	return script
}

// command is the command a container starts with once TLS is on.
func (e dbTLSEngine) command(original []string, require bool) []string {
	if e.redisServer != "" {
		return []string{"sh", "-c", redisTLSScript(e.redisServer, e.account)}
	}
	return append([]string{"sh", "-c", e.wrapScript(require), dbTLSMarker}, original...)
}

// plain is the command the container started with before TLS.
func (e dbTLSEngine) plain(tmpl provisionTemplate, original []string) []string {
	if e.redisServer != "" {
		return []string{"sh", "-c", tmpl.bootstrap}
	}
	return original
}

// readCommand takes the container's current command apart: whether TLS is
// already on, and what the command was before it. An error is a command this
// code did not write and does not recognise.
func (e dbTLSEngine) readCommand(tmpl provisionTemplate, spec *dockerx.ContainerSpec) (original []string, on bool, err error) {
	// Some images put an init (tini) in front of the entrypoint script.
	if n := len(spec.Entrypoint); n == 0 || path.Base(spec.Entrypoint[n-1]) != "docker-entrypoint.sh" {
		return nil, false, fmt.Errorf("it does not start through its image's entrypoint, so it was not set up from a template")
	}
	cmd := spec.Command
	if e.redisServer != "" {
		switch {
		case slices.Equal(cmd, []string{"sh", "-c", redisTLSScript(e.redisServer, e.account)}):
			return nil, true, nil
		case slices.Equal(cmd, []string{"sh", "-c", tmpl.bootstrap}):
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("its start command was changed after it was created")
	}
	if len(cmd) >= 4 && cmd[0] == "sh" && cmd[1] == "-c" && cmd[3] == dbTLSMarker {
		return cmd[4:], true, nil
	}
	if len(cmd) == 0 || !slices.Contains(e.servers, path.Base(cmd[0])) {
		return nil, false, fmt.Errorf("its start command was changed after it was created")
	}
	return cmd, false, nil
}

// extraFiles are delivered beside the certificate: PostgreSQL's rules when it is
// to refuse plaintext.
func (e dbTLSEngine) extraFiles(require bool) []dbTLSFile {
	if e.hba && require {
		return []dbTLSFile{{"pg_hba.conf", []byte(pgRequireHBA), 0o644}}
	}
	return nil
}
