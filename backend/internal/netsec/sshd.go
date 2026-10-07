package netsec

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// SSH is where a single-server operator is actually attacked.
//
// Every panel in this class shows the firewall and stops. The firewall decides
// who may knock; sshd decides what happens next, and its defaults are a
// compromise struck for compatibility across twenty years of clients rather
// than for a machine sitting on a public address. The three settings that
// matter — root login, password authentication and empty passwords — are one
// line each in a file nobody opens, and the difference between them being
// right and wrong is the difference between a bot wasting its time and a bot
// getting in.
//
// So this reads the *effective* configuration rather than the file: `sshd -T`
// resolves includes, defaults and the values sshd will really use, and a file
// that sets PasswordAuthentication twice does not read the way it looks.

// SSHDConfig is the running SSH server's configuration as it will behave.
type SSHDConfig struct {
	Available bool `json:"available"`
	// Source says where the answer came from, because the two are not equally
	// trustworthy: "sshd -T" is what the daemon computed, a file path is our
	// own reading of it.
	Source string `json:"source"`
	// Settings are the ones worth a control, each with what it is set to,
	// what it should be, and why.
	Settings []SSHSetting `json:"settings"`
	Ports    []string     `json:"ports"`
	// ManagedFile is where a change would be written. Naming it up front is
	// the difference between a settings page and an unmarked edit of a file
	// somebody else may be managing.
	ManagedFile string `json:"managedFile,omitempty"`
	// KeyedAccounts is who could still get in with password authentication
	// switched off. Turning it off with this list empty is a lockout, and it
	// is the commonest way people lock themselves out of a server.
	KeyedAccounts []KeyedAccount `json:"keyedAccounts"`
	// HasMatchBlocks warns that some of these values are overridden for some
	// users, which this page does not attempt to render.
	HasMatchBlocks bool `json:"hasMatchBlocks"`
	// Socket is a systemd socket unit holding SSH's listener. Where one is
	// active it owns the port and sshd_config's does nothing, which is the
	// difference between the port control working and reporting success while
	// changing nothing.
	Socket SSHSocket `json:"socket"`
	Error  string    `json:"error,omitempty"`
}

// SSHSetting is one directive, carrying the reasoning next to the value the
// same way a Docker finding does.
type SSHSetting struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
	// Recommended is what a public-facing host should have. Secure reports
	// whether the current value counts as that — a separate field because
	// several directives have more than one acceptable answer.
	Recommended string   `json:"recommended"`
	Secure      bool     `json:"secure"`
	Detail      string   `json:"detail"`
	Risk        string   `json:"risk,omitempty"`
	Options     []string `json:"options,omitempty"`
	// Kind is "choice" for a fixed set and "number" for a bounded integer,
	// so the UI knows which control to draw.
	Kind string `json:"kind"`
}

// KeyedAccount is an account with at least one authorized key.
type KeyedAccount struct {
	User string `json:"user"`
	Keys int    `json:"keys"`
}

// sshdCandidates are where sshd lives. The binary is in sbin, which is not on
// this process's PATH, and nsenter resolves the name with the PATH it inherits
// rather than the host's — so the absolute paths are tried first and the bare
// name is the fallback for a host that keeps it somewhere else.
var sshdCandidates = []string{"/usr/sbin/sshd", "/sbin/sshd", "/usr/local/sbin/sshd", "sshd"}

func sshdBinary() (string, bool) {
	for _, c := range sshdCandidates {
		if hostexec.Available(c) {
			return c, true
		}
	}
	return "", false
}

// sshConfigDir is the host's SSH configuration directory. The container mounts
// the host's /etc at the same path, so this is the real one.
const sshConfigDir = "/etc/ssh"

// managedDropIn is where this dashboard writes. A separate file rather than
// edits scattered through sshd_config: it can be read, diffed and deleted as
// one thing, and it says plainly which lines came from here.
const managedDropIn = "99-just-dashboard.conf"

// SSHDStatus reads the effective configuration.
func (s *Service) SSHDStatus(ctx context.Context) *SSHDConfig {
	cfg := &SSHDConfig{Settings: []SSHSetting{}, Ports: []string{}, KeyedAccounts: []KeyedAccount{}}
	bin, ok := sshdBinary()
	if !ok {
		if _, err := os.Stat(filepath.Join(sshConfigDir, "sshd_config")); err != nil {
			return cfg
		}
	}
	cfg.Available = true

	var values map[string][]string
	if ok {
		runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if out, err := hostexec.CommandOnHost(runCtx, bin, "-T").Output(); err == nil {
			values = parseSSHDDump(string(out))
			cfg.Source = bin + " -T"
		}
	}
	if values == nil {
		// sshd -T needs root and a valid config; neither is guaranteed. The
		// file is a worse answer — it does not resolve defaults — but it is
		// an answer, and saying which one this is matters more than hiding it.
		parsed, match, err := parseSSHDFiles(sshConfigDir)
		if err != nil {
			cfg.Error = err.Error()
			return cfg
		}
		values, cfg.HasMatchBlocks = parsed, match
		cfg.Source = filepath.Join(sshConfigDir, "sshd_config")
	}

	// A Match block makes some of these values conditional. `sshd -T` reports
	// the global answer and says nothing about the exceptions, so the files
	// are read for that fact whichever source supplied the values — reading it
	// only on the fallback path meant the warning never appeared on the host
	// where it mattered, since that is the path that almost never runs.
	if !cfg.HasMatchBlocks {
		if _, match, err := parseSSHDFiles(sshConfigDir); err == nil {
			cfg.HasMatchBlocks = match
		}
	}

	cfg.Socket = readSSHSocket(ctx)
	cfg.Ports = values["port"]
	if len(cfg.Ports) == 0 {
		cfg.Ports = []string{"22"}
	}
	// The socket's port is the one connections actually arrive on.
	if len(cfg.Socket.Ports) > 0 {
		cfg.Ports = cfg.Socket.Ports
	}
	for _, def := range sshDirectives {
		value := def.canonical(first(values[def.Key], def.Default))
		if def.Key == "port" && len(cfg.Socket.Ports) > 0 {
			// On a socket-activated host sshd_config's Port is read, resolved,
			// reported by `sshd -T` and ignored. Showing it in the control
			// meant the header said "Port 2222 · held by ssh.socket" while the
			// field beneath it said 22 — and the apply path, which compares
			// against the socket, then read a save of that displayed value as
			// a request to move SSH back onto it.
			value = cfg.Socket.Ports[0]
		}
		if def.Kind == "list" {
			// sshd accumulates these across every line that sets them, so the
			// first value alone would silently hide the rest.
			value = strings.Join(values[def.Key], " ")
		}
		cfg.Settings = append(cfg.Settings, SSHSetting{
			Key: def.Key, Label: def.Label, Value: value,
			Recommended: def.Recommended, Secure: def.secure(value),
			Detail: def.Detail, Risk: def.Risk, Options: def.Options, Kind: def.Kind,
		})
	}
	cfg.ManagedFile = filepath.Join(sshConfigDir, "sshd_config.d", managedDropIn)
	if !includesDropIns(sshConfigDir) {
		cfg.ManagedFile = filepath.Join(sshConfigDir, "sshd_config")
	}
	cfg.KeyedAccounts = keyedAccounts()
	return cfg
}

func first(values []string, fallback string) string {
	if len(values) == 0 {
		return fallback
	}
	return values[0]
}

// sshDirective describes one setting this page offers.
type sshDirective struct {
	Key         string
	Label       string
	Default     string
	Recommended string
	// Accept lists every value that counts as secure. More than one because
	// "prohibit-password" and "forced-commands-only" are both fine answers
	// for root login and neither is "the" recommendation.
	Accept []string
	// Max and Min are the *recommendation*: a MaxAuthTries of 6 is legal and
	// not advised. LegalMax and LegalMin are what the value is refused
	// outside. Conflating the two meant a port's range was treated as advice
	// and never enforced.
	Max      int
	Min      int
	LegalMax int
	LegalMin int
	Detail   string
	Risk     string
	Options  []string
	Kind     string
	// Aliases maps a spelling sshd still prints onto the one this page's
	// control offers. They are the same setting; a value the dropdown has no
	// entry for renders as an empty control, which reads as "not set" for a
	// setting that very much is.
	Aliases map[string]string
	// AlwaysAcceptable marks a directive with no better or worse value — a
	// port, a list of accounts. Grading those would put a permanent amber
	// dot next to a setting that is simply a choice.
	AlwaysAcceptable bool
}

// canonical folds a spelling sshd prints onto the one this page offers.
func (d sshDirective) canonical(value string) string {
	if to, ok := d.Aliases[strings.ToLower(strings.TrimSpace(value))]; ok {
		return to
	}
	return value
}

func (d sshDirective) secure(value string) bool {
	value = strings.ToLower(strings.TrimSpace(d.canonical(value)))
	if d.AlwaysAcceptable {
		return true
	}
	if d.Kind == "number" {
		n, err := strconv.Atoi(value)
		if err != nil {
			return false
		}
		if d.Max > 0 && n > d.Max {
			return false
		}
		if d.Min > 0 && n < d.Min {
			return false
		}
		return true
	}
	for _, a := range d.Accept {
		if value == a {
			return true
		}
	}
	return false
}

// sshDirectives is the closed set. Closed because every entry here becomes a
// line this dashboard is willing to write into sshd_config, and an open set
// would make this endpoint a config editor with extra steps — one that can
// take the machine off the network.
var sshDirectives = []sshDirective{
	{
		Key: "permitrootlogin", Label: "Root login", Default: "prohibit-password",
		Recommended: "prohibit-password", Accept: []string{"no", "prohibit-password", "forced-commands-only"},
		Kind: "choice", Options: []string{"no", "prohibit-password", "forced-commands-only", "yes"},
		// `sshd -T` on OpenSSH 9.9 — Ubuntu 25.04, which is to say a current
		// mainstream server — still prints the deprecated spelling for the
		// value the distributions actually ship as their default. The two are
		// the same setting.
		Aliases: map[string]string{"without-password": "prohibit-password"},
		Detail:  "Whether root may log in over SSH at all, and if so how.",
		Risk:    "\"yes\" lets every bot on the internet guess at the one account that already has full control. Every one of them tries root first.",
	},
	{
		Key: "passwordauthentication", Label: "Password authentication", Default: "yes",
		Recommended: "no", Accept: []string{"no"},
		Kind: "choice", Options: []string{"no", "yes"},
		Detail: "Whether a password alone is enough to get a shell.",
		Risk:   "With this on, your server's security is whatever the weakest password on it is. Keys cannot be guessed.",
	},
	{
		Key: "pubkeyauthentication", Label: "Public key authentication", Default: "yes",
		Recommended: "yes", Accept: []string{"yes"},
		Kind: "choice", Options: []string{"yes", "no"},
		Detail: "Whether keys are accepted. This is the way in that replaces passwords.",
		Risk:   "Turning this off with passwords already disabled leaves nobody a way in.",
	},
	{
		Key: "permitemptypasswords", Label: "Empty passwords", Default: "no",
		Recommended: "no", Accept: []string{"no"},
		Kind: "choice", Options: []string{"no", "yes"},
		Detail: "Whether an account with a blank password may log in.",
		Risk:   "There is no legitimate reason for this on a server reachable from anywhere.",
	},
	{
		Key: "x11forwarding", Label: "X11 forwarding", Default: "no",
		Recommended: "no", Accept: []string{"no"},
		Kind: "choice", Options: []string{"no", "yes"},
		Detail: "Lets a remote session open windows on your desktop.",
		Risk:   "Rarely wanted on a server, and it widens what a compromised session can reach.",
	},
	{
		Key: "allowtcpforwarding", Label: "TCP forwarding", Default: "yes",
		// Not graded: a jump host exists to forward TCP, and "no" there takes
		// the host's one job away. Everywhere else it is the setting that
		// turns a stolen key into a tunnel into the internal network, which
		// is what the recommendation says. "local" is the middle answer for
		// a bastion: ProxyJump needs it, remote forwards (-R) do not.
		Recommended: "no, unless this server is a jump host", Kind: "choice", AlwaysAcceptable: true,
		Options: []string{"no", "local", "remote", "all", "yes"},
		Detail:  "Whether a login may open TCP tunnels through this server (ssh -L and -R, and ProxyJump).",
		Risk:    "With forwarding on, anyone holding a key can reach whatever this server can reach, from outside the firewall. A jump host needs yes or local; a server that is not one should say no.",
	},
	{
		Key: "gatewayports", Label: "Forwarded ports on all interfaces", Default: "no",
		Recommended: "no", Accept: []string{"no"},
		Kind: "choice", Options: []string{"no", "clientspecified", "yes"},
		Detail: "Whether a port forwarded with -R listens on this server's public addresses or only on loopback.",
		Risk:   "With yes, a remote forward is open to the whole network instead of the one session that asked for it.",
	},
	{
		Key: "allowagentforwarding", Label: "Agent forwarding", Default: "yes",
		Recommended: "no", Accept: []string{"no"},
		Kind: "choice", Options: []string{"no", "yes"},
		Detail: "Whether a login may use the SSH agent on the machine it came from, from this server.",
		Risk:   "Anyone with root here can use the forwarded agent to sign in as you elsewhere for as long as you are connected. ProxyJump reaches the same hosts without lending out the agent.",
	},
	{
		Key: "permittunnel", Label: "Tunnel devices", Default: "no",
		Recommended: "no", Accept: []string{"no"},
		Kind: "choice", Options: []string{"no", "point-to-point", "ethernet", "yes"},
		Detail: "Whether a login may create a tun or tap device on this server, which is a VPN over SSH.",
		Risk:   "A tunnel device puts the client on this server's network at layer 3 or 2, past anything that filters TCP forwards.",
	},
	{
		Key: "maxsessions", Label: "Sessions per connection", Default: "10",
		// A multiplexed connection (ControlMaster) shares one login across its
		// sessions, so a bastion serving many people through one master
		// wants more and a hardened server fewer; neither is wrong.
		Recommended: "any", LegalMin: 1, LegalMax: 64, Kind: "number", AlwaysAcceptable: true,
		Detail: "How many shells, forwards and file transfers one login may have open at once.",
		Risk:   "Lowering it below what automation multiplexes over one connection makes the extra sessions fail with an error that does not mention this setting.",
	},
	{
		Key: "maxauthtries", Label: "Attempts per connection", Default: "6",
		Recommended: "3 or fewer", Max: 4, Kind: "number",
		Detail: "How many guesses one connection gets before it is dropped.",
		Risk:   "The default of six lets a bot try six passwords per handshake instead of one.",
	},
	{
		Key: "logingracetime", Label: "Login grace time", Default: "120",
		Recommended: "60 seconds or less", Max: 60, Kind: "number",
		Detail: "How long an unauthenticated connection may stay open.",
		Risk:   "A long grace time lets a small number of connections hold every available slot.",
	},
	{
		Key: "clientaliveinterval", Label: "Idle check interval", Default: "0",
		Recommended: "300 seconds", Min: 1, Max: 900, Kind: "number",
		Detail: "How often the server checks a quiet session is still there.",
		Risk:   "At zero, a session whose client vanished stays open indefinitely.",
	},
	{
		Key: "port", Label: "Port", Default: "22",
		// Any port is as secure as any other. Moving off 22 removes the
		// background noise of untargeted scanning and nothing else, which is
		// worth saying rather than dressing up as hardening — so it is not
		// graded, only bounded.
		Recommended: "any", LegalMin: 1, LegalMax: 65535,
		Kind: "number", AlwaysAcceptable: true,
		Detail: "Where sshd listens. Moving off 22 quiets the logs; it does not make the server harder to break into.",
		Risk:   "Change this and the firewall must already allow the new port, or the next connection has nowhere to land.",
	},
	{
		Key: "allowusers", Label: "Only these accounts may log in", Default: "",
		Recommended: "", Kind: "list", AlwaysAcceptable: true,
		Detail: "A space-separated list. Leave empty to allow every account that is otherwise permitted.",
		Risk:   "An account left off this list cannot log in at all, however good its key is.",
	},
	{
		Key: "denyusers", Label: "These accounts may never log in", Default: "",
		Recommended: "", Kind: "list", AlwaysAcceptable: true,
		Detail: "A space-separated list, applied before the allow list.",
	},
}

// parseSSHDDump reads `sshd -T`, whose output is one lowercase directive per
// line. Repeated keys accumulate in order — port and hostkey are the usual
// ones — so the value is a slice and callers take the first.
func parseSSHDDump(out string) map[string][]string {
	values := map[string][]string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		key = strings.ToLower(key)
		values[key] = append(values[key], strings.TrimSpace(value))
	}
	return values
}

// parseSSHDFiles reads sshd_config and whatever it includes.
//
// Two things about sshd's own semantics are load-bearing and get read
// backwards by anyone parsing this file as if it were an ini: the **first**
// value for a keyword wins, not the last, and everything after a Match block
// applies conditionally. So an earlier value is never overwritten, and a Match
// ends the file as far as this reader is concerned — with the fact reported,
// because "there are conditional overrides" is information, not something to
// silently drop.
func parseSSHDFiles(dir string) (map[string][]string, bool, error) {
	values := map[string][]string{}
	matched := false
	main := filepath.Join(dir, "sshd_config")
	if err := readSSHDFile(main, dir, values, &matched, 0); err != nil {
		return nil, false, err
	}
	return values, matched, nil
}

func readSSHDFile(path, dir string, values map[string][]string, matched *bool, depth int) error {
	// sshd itself refuses deeply nested includes; the bound here is against a
	// loop rather than against depth being meaningful.
	if depth > 8 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8192), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			// sshd also accepts "Key=value".
			key, value, ok = strings.Cut(line, "=")
			if !ok {
				continue
			}
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"`))
		if key == "match" {
			*matched = true
			return nil
		}
		if key == "include" {
			for _, pattern := range strings.Fields(value) {
				if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(dir, pattern)
				}
				includes, _ := filepath.Glob(pattern)
				sort.Strings(includes)
				for _, inc := range includes {
					readSSHDFile(inc, dir, values, matched, depth+1)
				}
			}
			continue
		}
		values[key] = append(values[key], value)
	}
	return sc.Err()
}

// includesDropIns reports whether sshd_config pulls in sshd_config.d before it
// sets anything itself.
//
// Position is the whole question. First value wins, so a drop-in included at
// the top of the file overrides everything below it — which is what the
// distributions arrange and why writing there is clean. A drop-in included at
// the *bottom*, or not at all, would be a file this dashboard wrote and sshd
// ignored, which is the worst possible outcome for a security setting.
func includesDropIns(dir string) bool {
	f, err := os.Open(filepath.Join(dir, "sshd_config"))
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		if strings.EqualFold(key, "include") && strings.Contains(value, "sshd_config.d") {
			return true
		}
		// Any other directive reached first means a drop-in would lose.
		return false
	}
	return false
}

// keyedAccounts lists the accounts that have an authorized_keys file with
// something in it. This is what makes disabling password authentication safe
// or catastrophic, so it is read from the host rather than assumed.
func keyedAccounts() []KeyedAccount {
	out := []KeyedAccount{}
	for _, base := range []string{"", "/host"} {
		f, err := os.Open(base + "/etc/passwd")
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Split(sc.Text(), ":")
			if len(fields) < 6 || fields[5] == "" {
				continue
			}
			keys := countAuthorizedKeys(base + filepath.Join(fields[5], ".ssh", "authorized_keys"))
			if keys > 0 {
				out = append(out, KeyedAccount{User: fields[0], Keys: keys})
			}
		}
		f.Close()
		break
	}
	sort.Slice(out, func(i, j int) bool { return out[i].User < out[j].User })
	return out
}

func countAuthorizedKeys(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8192), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			n++
		}
	}
	return n
}
