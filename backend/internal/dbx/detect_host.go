package dbx

import "strings"

// Recognising a database server that is not in a container.
//
// The Docker half of detect.go can read a container's environment, so it knows
// the credentials and can finish the job. Nothing here can: a Postgres that
// apt installed keeps its passwords in its own catalogue and its rules in
// pg_hba.conf, and no amount of reading the process tells this dashboard what
// to send. Guessing is not an option either — a wrong password against the
// operator's own server is an authentication failure in their logs and, on a
// host with fail2ban, a step towards banning this dashboard.
//
// So the answer here is narrower and honest: say the server is there, say what
// it is and where, and ask for the one thing that genuinely cannot be known.
// That is still the whole of the fix, because the failure it replaces is a
// Databases page that showed a server's containerised neighbours and stayed
// silent about the one running natively beside them.
//
// The signal is the *process*, not the port. A Postgres moved to 5433 is
// ordinary and a stranger on 5432 is not a Postgres, so matching a port would
// be wrong in both directions; the listening process is what the machine
// actually states about itself.

// Source says where a candidate was found, because the two are adopted
// differently and it is the difference the operator sees.
const (
	SourceDocker = "docker"
	SourceHost   = "host"
)

// HostListener is one listening socket, in the terms this package needs. The
// caller supplies them — proxysvc already enumerates sockets joined to their
// processes, and dbx has no business opening /proc.
type HostListener struct {
	Protocol string
	Address  string
	Port     int
	Process  string
	User     string
	// PID groups the sockets one server holds. Cmdline tells apart the
	// programs that share a name, and names the product where the process is
	// only a runtime. Manager and ManagerName say who supervises it — systemd
	// and a unit, or a container and its id — as the cgroup states it.
	PID         int32
	Cmdline     string
	Manager     string
	ManagerName string
}

// DetectHost recognises a database server from a listening socket, returning
// nil for a socket that is not one this dashboard knows how to speak to.
//
// A container's published port is deliberately not matched here: the host sees
// it as `docker-proxy`, which is in no rule, so the Docker half stays the one
// place a container is described. A container on the host's own network
// namespace is the exception — it looks exactly like a native server, which is
// why the caller de-duplicates by address before adopting anything.
func DetectHost(l HostListener) *Candidate {
	if l.Protocol != "" && !strings.EqualFold(l.Protocol, "tcp") {
		return nil
	}
	if l.Port <= 0 {
		return nil
	}
	// The name is matched exactly, allowing for the kernel cutting it at 15
	// characters. A prefix match read "postgres_exporter" as a Postgres.
	p := productForProcess(l.Process, l.Cmdline)
	if p == nil || p.driver == "" || p.sidePort(l.Port) {
		// A socket the server holds that its driver cannot speak to — MySQL's
		// X protocol, ClickHouse's HTTP interface — is not a way in.
		return nil
	}
	return &Candidate{
		Driver:           p.driver,
		Source:           SourceHost,
		Process:          l.Process,
		Host:             hostAddress(l.Address),
		Port:             l.Port,
		User:             p.user,
		Database:         p.database,
		NeedsCredentials: !p.open,
	}
}

// HostConnectionName is what a server found this way is called in the picker.
//
// The process name would be the obvious choice and is the wrong one: "mysqld"
// and "postmaster" name the program rather than the thing being connected to,
// and a list mixing them with container names reads as a list of unrelated
// objects. Saying where it is instead is what distinguishes it from the
// container next to it running the same engine.
func HostConnectionName(c Candidate) string {
	return string(c.Driver) + " on this host"
}
