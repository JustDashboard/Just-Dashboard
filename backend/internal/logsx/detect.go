package logsx

import (
	"path/filepath"
	"strings"
)

// LensTarget is what is known about a source before its first line is read:
// the kind, and a path, an image or a unit depending on the kind. For a
// journal-id source, Unit carries the first identifier.
type LensTarget struct {
	Kind  SourceKind
	Path  string
	Image string
	Unit  string
}

// DetectLens names the lens a source reads through when the operator has not
// forced one. It answers only lenses this build registers, so a caller can put
// the answer straight into Filter.Lens; a source whose lens is not built reads
// the way every source did before lenses existed.
//
// Detection runs in the handlers, never inside the search or the tail: those
// read Filter.Lens and nothing else, which is what keeps the package's own
// tests describing the behaviour they always did.
func DetectLens(t LensTarget) string {
	id := detectLensID(t)
	if lens, _ := LensByID(id); lens == nil {
		return ""
	}
	return id
}

func detectLensID(t LensTarget) string {
	switch t.Kind {
	case KindDocker:
		return ImageLens(t.Image)
	case KindPM2:
		return "pm2"
	case KindJournal:
		if t.Unit == "" {
			// The whole journal is every program on the host at once; the
			// syslog lens hands each line to the lens its program has.
			return "syslog"
		}
		return unitLens(t.Unit)
	case KindJournalID:
		if id := ProgramLens(t.Unit); id != "" {
			return id
		}
		return unitLens(t.Unit)
	case KindKernel:
		return "kernel"
	case KindStack:
		// A stack has one lens per container; there is no one answer.
		return ""
	}
	if t.Path != "" {
		return fileLens(t.Path)
	}
	return ""
}

// fileLens decides by directory and basename, never by a substring of the
// name: /var/log/myapp/cronjobs.log is an application's log, not cron's.
func fileLens(p string) string {
	p = filepath.Clean(p)
	dir, base := filepath.Dir(p)+"/", filepath.Base(p)
	under := func(d string) bool { return strings.HasPrefix(dir, d) }
	switch {
	case under("/var/log/postgresql/"):
		return "postgres"
	case under("/var/log/mysql/"), under("/var/log/mariadb/"):
		return "mysql"
	case under("/var/log/redis/"), under("/var/log/valkey/"):
		return "redis"
	case under("/var/log/mongodb/"):
		return "mongodb"
	case under("/var/log/clickhouse-server/"):
		return "clickhouse"
	case under("/var/log/nginx/"), under("/var/log/apache2/"), under("/var/log/httpd/"):
		switch {
		case strings.Contains(base, "access"):
			return "http-access"
		case strings.Contains(base, "error"):
			return "nginx-error"
		}
		return "app"
	case under("/var/log/caddy/"):
		if strings.Contains(base, "access") {
			return "http-access"
		}
		return "caddy"
	case under("/var/log/letsencrypt/"):
		return "certbot"
	case under("/var/log/apt/"):
		return "packages"
	}
	switch base {
	case "auth.log", "secure":
		return "auth"
	case "ufw.log":
		return "firewall"
	case "kern.log":
		return "kernel"
	case "fail2ban.log":
		return "fail2ban"
	case "dpkg.log":
		return "packages"
	case "syslog", "messages":
		return "syslog"
	case "cron", "cron.log":
		return "cron"
	}
	if strings.HasPrefix(base, "unattended-upgrades") && strings.HasSuffix(base, ".log") {
		return "packages"
	}
	if strings.HasPrefix(base, "dnf") && strings.HasSuffix(base, ".log") {
		return "packages"
	}
	return "app"
}

// ImageLens reads a container image reference: the repository's last path
// segment, matched by prefix, so postgres:17, docker.io/library/postgres,
// bitnami/postgresql and ghcr.io/x/timescaledb-ha all land on the same lens.
// It is looser than the database detector on purpose — naming a lens wrongly
// costs a few unlabelled lines, where calling a container a database it is
// not would offer to connect to it.
func ImageLens(image string) string {
	ref := strings.ToLower(strings.TrimSpace(image))
	if i := strings.IndexByte(ref, '@'); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndexByte(ref, ':'); i > strings.LastIndexByte(ref, '/') {
		ref = ref[:i]
	}
	switch {
	case strings.HasSuffix(ref, "supabase/postgres"):
		return "postgres"
	case strings.HasSuffix(ref, "mssql/server"):
		return "mssql"
	}
	name := ref[strings.LastIndexByte(ref, '/')+1:]
	for _, rule := range imageRules {
		for _, prefix := range rule.prefixes {
			if strings.HasPrefix(name, prefix) {
				return rule.lens
			}
		}
	}
	return "app"
}

var imageRules = []struct {
	lens     string
	prefixes []string
}{
	{"postgres", []string{"postgres", "postgis", "pgvector", "timescaledb"}},
	{"mysql", []string{"mysql", "mariadb", "percona"}},
	{"redis", []string{"redis", "valkey", "keydb"}},
	{"mongodb", []string{"mongo"}},
	{"clickhouse", []string{"clickhouse"}},
	{"mssql", []string{"azure-sql-edge"}},
	{"nginx", []string{"nginx", "openresty"}},
	{"caddy", []string{"caddy"}},
}

// unitLens reads a systemd unit's name. Only the service's own name is
// consulted — a unit called "api.service" is an application whatever it
// runs, and guessing from its description would be guessing.
func unitLens(unit string) string {
	name := strings.ToLower(strings.TrimSuffix(unit, ".service"))
	switch name {
	case "ssh", "sshd":
		return "auth"
	case "fail2ban":
		return "fail2ban"
	case "cron", "crond":
		return "cron"
	case "nginx":
		return "nginx-error"
	case "caddy":
		return "caddy"
	case "unattended-upgrades":
		return "packages"
	}
	for _, rule := range unitRules {
		for _, prefix := range rule.prefixes {
			if strings.HasPrefix(name, prefix) {
				return rule.lens
			}
		}
	}
	return "app"
}

var unitRules = []struct {
	lens     string
	prefixes []string
}{
	{"postgres", []string{"postgresql"}},
	{"mysql", []string{"mysql", "mariadb"}},
	{"redis", []string{"redis", "valkey"}},
	{"mongodb", []string{"mongod"}},
	{"clickhouse", []string{"clickhouse"}},
	{"mssql", []string{"mssql"}},
	{"certbot", []string{"certbot"}},
	{"packages", []string{"apt-daily"}},
}

// ProgramLens names the lens a syslog identifier's lines read through, or ""
// when the program has none of its own. It is how one journal run holds two
// kinds of line: the manager's "Started postgresql@17-main.service" and the
// postmaster's own output, or sshd's refusals inside ssh.service. The list is
// the identifiers these programs actually log under — OpenSSH 9.8 moved almost
// every sshd line to sshd-session, and matching only "sshd" misses nearly all
// of them.
func ProgramLens(program string) string {
	p := strings.ToLower(program)
	switch p {
	case "sshd", "sshd-session", "sshd-auth", "sudo", "su", "login", "systemd-logind",
		"useradd", "usermod", "userdel", "passwd", "chpasswd", "groupadd", "groupdel":
		return "auth"
	case "cron", "crond":
		return "cron"
	case "kernel":
		return "kernel"
	case "systemd", "systemd-coredump":
		return "systemd"
	case "certbot":
		return "certbot"
	case "mysqld", "mariadbd":
		return "mysql"
	case "nginx":
		return "nginx-error"
	}
	switch {
	case strings.HasPrefix(p, "fail2ban"):
		return "fail2ban"
	case strings.HasPrefix(p, "postgres"):
		return "postgres"
	case strings.HasPrefix(p, "redis"), strings.HasPrefix(p, "valkey"):
		return "redis"
	}
	return ""
}
