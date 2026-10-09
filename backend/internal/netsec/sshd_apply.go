package netsec

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Applying an SSH change from a web page is the most dangerous thing this
// dashboard does, because getting it wrong removes the way back in.
//
// So the order is the same one the proxy editor uses, for the same reason:
// refuse the changes that are certainly a lockout, write, validate with the
// daemon's own parser, restore on failure, and only then reload. sshd's `-t`
// is the equivalent of `nginx -t` and it is the difference between a typo
// costing a reload and a typo costing the machine.

var ErrSSHInvalid = fmt.Errorf("sshd rejected the configuration")

// SSHApplyPlan is a validated change, ready to write.
//
// Applying is streamed now, which means the two halves have to come apart: the
// checks answer the request — a lockout is a 409 the operator sees at once,
// not a job that fails a second later — while the write, the `sshd -t` and the
// reload belong to a job whose output is worth watching.
type SSHApplyPlan struct {
	// File is where the change lands, named up front because a settings page
	// that edits a file without saying which is an unmarked edit.
	File string
	// Content is the whole new file, already merged.
	Content string
	// Applied lists the directives being set, for the audit entry.
	Applied []string
	// Socket is the systemd socket unit that owns SSH's listener, where one
	// does. SocketPort is the port it has to be moved onto — writing the
	// directive alone on such a host changes a value sshd never consults.
	Socket     SSHSocket
	SocketPort string

	original string
	existed  bool
}

// PlannedFile is one file an apply writes, with its whole new content.
type PlannedFile struct {
	Path    string
	Content string
}

// Files names every file ApplySSHPlan will write, so a caller enrolling the
// apply in a recovery journal can snapshot each one before the first write.
func (p *SSHApplyPlan) Files() []PlannedFile {
	files := []PlannedFile{{Path: p.File, Content: p.Content}}
	if p.SocketPort != "" && p.Socket.DropIn != "" {
		files = append(files, PlannedFile{
			Path:    p.Socket.DropIn,
			Content: socketDropIn(p.Socket.Unit, p.SocketPort, p.Socket.Listen),
		})
	}
	return files
}

// SocketUnit is the socket unit the apply restarts, or empty.
func (p *SSHApplyPlan) SocketUnit() string {
	if p.SocketPort == "" {
		return ""
	}
	return p.Socket.Unit
}

// Failure is the reason a finished apply did not leave the requested
// configuration in force. A written file that sshd did not pick up, or a
// socket that kept its old port, is a success for an immediate apply's report
// and a failure for a pending one, which must then restore the previous state.
func (r *SSHApplyResult) Failure() error {
	switch {
	case r == nil:
		return fmt.Errorf("the SSH apply did not run")
	case r.SocketError != "":
		return fmt.Errorf("%s did not move to the new port: %s", r.SocketUnit, r.SocketError)
	case r.ReloadError != "":
		return fmt.Errorf("sshd did not reload: %s", r.ReloadError)
	}
	return nil
}

// SSHApplyResult reports what happened, in the order it happened, so a
// partial success reads as one.
type SSHApplyResult struct {
	Written  bool   `json:"written"`
	File     string `json:"file"`
	Valid    bool   `json:"valid"`
	Output   string `json:"output,omitempty"`
	Reloaded bool   `json:"reloaded"`
	// ReloadError is set when the file is good but the daemon would not pick
	// it up. That is a real state and pretending otherwise would leave the
	// operator believing a setting is in force when it is not.
	ReloadError string   `json:"reloadError,omitempty"`
	Applied     []string `json:"applied"`
	// SocketMoved reports that the systemd socket unit was moved onto the new
	// port as well, which on a socket-activated host is the half that actually
	// changes where SSH answers.
	SocketMoved bool   `json:"socketMoved,omitempty"`
	SocketUnit  string `json:"socketUnit,omitempty"`
	SocketError string `json:"socketError,omitempty"`
}

var (
	sshValueRe = regexp.MustCompile(`^[A-Za-z0-9._:/@,\-]{1,64}$`)
	// A list of accounts needs spaces, which the scalar pattern refuses on
	// purpose. Usernames are checked individually rather than the whole line
	// being loosened.
	sshUserRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,32}$`)
)

// PlanSSHSettings validates a change and works out the file it produces.
//
// Nothing is written here. Every refusal this function can make — an unknown
// directive, a value out of range, any of the lockouts — is one the caller
// should hear about in the response to its own request.
func (s *Service) PlanSSHSettings(ctx context.Context, changes map[string]string) (*SSHApplyPlan, error) {
	if len(changes) == 0 {
		return nil, fmt.Errorf("no settings given")
	}
	clean := map[string]string{}
	for key, value := range changes {
		def, ok := sshDirectiveFor(key)
		if !ok {
			return nil, fmt.Errorf("%q is not a setting this dashboard manages", key)
		}
		value = strings.TrimSpace(value)
		if def.Kind != "list" {
			value = strings.ToLower(value)
			if !sshValueRe.MatchString(value) {
				return nil, fmt.Errorf("invalid value for %s", def.Label)
			}
		}
		if err := validateSSHValue(def, value); err != nil {
			return nil, err
		}
		if def.Kind == "list" {
			// Collapsed to single spaces so the line written is exactly the
			// tokens that were validated, and nothing between them.
			value = strings.Join(strings.Fields(value), " ")
		}
		clean[def.Key] = value
	}

	current := s.SSHDStatus(ctx)
	if !current.Available {
		return nil, fmt.Errorf("no SSH server configuration found on this host")
	}
	if err := guardSSHLockout(current, clean); err != nil {
		return nil, err
	}
	socketPort := ""
	if port, ok := clean["port"]; ok && port != first(current.Ports, "22") {
		// Moving the port with no firewall rule for the new one is the same
		// class of mistake as turning off passwords with no key: the change
		// applies, the daemon reloads, and the next connection has nowhere to
		// land. The firewall is the only place that can answer, so it is asked.
		if fw, err := s.Status(ctx); err == nil {
			if err := guardSSHPort(port, fw); err != nil {
				return nil, err
			}
		}
		// On a socket-activated host the directive is not what decides where
		// SSH answers, so the socket unit is moved too. Refusing instead would
		// leave the control broken on Ubuntu 24.04, which is the commonest
		// server this runs on; writing only the directive is worse still,
		// because it reports success and moves nothing.
		if current.Socket.Active() {
			socketPort = port
		}
	}

	target := current.ManagedFile
	original, existed := readFileOrEmpty(target)
	return &SSHApplyPlan{
		File:       target,
		Content:    applyDirectives(original, clean, filepath.Base(target) == managedDropIn),
		Applied:    sortedKeys(clean),
		Socket:     current.Socket,
		SocketPort: socketPort,
		original:   original,
		existed:    existed,
	}, nil
}

// ApplySSHPlan writes, validates and reloads, narrating as it goes.
//
// The order is the proxy editor's and for the same reason: write, test with
// the daemon's own parser, put the file back on failure, reload only then.
// `sshd -t` is the difference between a typo costing a reload and a typo
// costing the machine.
func (s *Service) ApplySSHPlan(ctx context.Context, plan *SSHApplyPlan, out LineWriter) (*SSHApplyResult, error) {
	res := &SSHApplyResult{File: plan.File, Applied: plan.Applied}
	out.Status("Writing %s", plan.File)
	if err := writeSSHFile(plan.File, plan.Content); err != nil {
		return res, err
	}
	res.Written = true

	out.Status("Testing the configuration with sshd -t")
	valid, output := sshdTest(ctx)
	res.Valid, res.Output = valid, output
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line != "" {
			out.Line("stderr", line)
		}
	}
	if !valid {
		out.Status("Rejected — putting the previous file back")
		restoreSSHFile(plan.File, plan.original, plan.existed)
		return res, ErrSSHInvalid
	}

	// The socket first, because it is the half that decides where connections
	// land. Failing here leaves sshd_config saying one thing and the machine
	// doing another, which is a state the operator has to be told about rather
	// than one to report as a clean apply.
	if plan.SocketPort != "" {
		res.SocketUnit = plan.Socket.Unit
		if err := applySocketPort(ctx, plan.Socket, plan.SocketPort, out); err != nil {
			res.SocketError = err.Error()
			out.Line("stderr", err.Error())
		} else {
			res.SocketMoved = true
		}
	}

	out.Status("Reloading sshd")
	if err := reloadSSH(ctx); err != nil {
		// The file is good; the daemon would not pick it up. That is a real
		// state, and pretending otherwise would leave the operator believing
		// a setting is in force when it is not.
		res.ReloadError = err.Error()
		out.Line("stderr", err.Error())
		return res, nil
	}
	res.Reloaded = true
	out.Status("Done. Existing sessions are untouched by a reload — check a second terminal before closing this one.")
	return res, nil
}

// LineWriter is the narrow slice of a job's emitter this package needs. Taking
// an interface rather than importing the jobs package keeps netsec free of a
// dependency on how its output is transported.
type LineWriter interface {
	Status(format string, args ...any)
	Line(stream, text string)
}

// ApplySSHSettings is plan-then-apply in one call, for callers with nowhere to
// stream to.
func (s *Service) ApplySSHSettings(ctx context.Context, changes map[string]string) (*SSHApplyResult, error) {
	plan, err := s.PlanSSHSettings(ctx, changes)
	if err != nil {
		return nil, err
	}
	return s.ApplySSHPlan(ctx, plan, discardLines{})
}

type discardLines struct{}

func (discardLines) Status(string, ...any) {}
func (discardLines) Line(string, string)   {}

func sshDirectiveFor(key string) (sshDirective, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, d := range sshDirectives {
		if d.Key == key {
			return d, true
		}
	}
	return sshDirective{}, false
}

// validateSSHValue keeps a directive to the values its own control offers. A
// number is bounded generously — the recommendation is stricter than what is
// allowed, because refusing a legal setting because we would not have chosen
// it is not this endpoint's job.
func validateSSHValue(def sshDirective, value string) error {
	if def.Kind == "list" {
		// An empty list means "no restriction", which is expressed by removing
		// the directive rather than by writing an empty one — sshd refuses a
		// keyword with no argument.
		if value == "" {
			return nil
		}
		// A newline inside the value would put a directive of the caller's
		// choosing into sshd_config on the line after this one. Fields would
		// happily split on it, so it is refused explicitly rather than
		// normalised away and forgotten.
		if strings.ContainsAny(value, "\n\r") {
			return fmt.Errorf("%s must be a single line", def.Label)
		}
		fields := strings.Fields(value)
		if len(fields) > 64 {
			return fmt.Errorf("%s: too many accounts", def.Label)
		}
		for _, name := range fields {
			if !sshUserRe.MatchString(name) {
				return fmt.Errorf("%q is not a valid account name", name)
			}
		}
		return nil
	}
	if def.Kind == "number" {
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s must be a number", def.Label)
		}
		lo, hi := 0, 86400
		if def.LegalMin > 0 || def.LegalMax > 0 {
			lo, hi = def.LegalMin, def.LegalMax
		}
		if n < lo || n > hi {
			return fmt.Errorf("%s must be between %d and %d", def.Label, lo, hi)
		}
		return nil
	}
	for _, opt := range def.Options {
		if value == opt {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s", def.Label, strings.Join(def.Options, ", "))
}

// guardSSHLockout refuses the changes that leave nobody a way in.
//
// The same reasoning as the firewall's guard, and the same narrowness: only
// the certain cases are refused. Disabling passwords on a host where somebody
// has an authorized key is the correct thing to do and must not be blocked;
// doing it on a host where nobody has one is the single commonest way people
// lose a server they were in the middle of securing.
func guardSSHLockout(current *SSHDConfig, changes map[string]string) error {
	effective := func(key string) string {
		if v, ok := changes[key]; ok {
			return v
		}
		for _, s := range current.Settings {
			if s.Key == key {
				return strings.ToLower(s.Value)
			}
		}
		return ""
	}
	password := effective("passwordauthentication")
	pubkey := effective("pubkeyauthentication")

	if password == "no" && pubkey == "no" {
		return fmt.Errorf("%w: with both passwords and keys refused, nothing could authenticate", ErrLockout)
	}
	if password == "no" && len(current.KeyedAccounts) == 0 {
		return fmt.Errorf("%w: no account on this host has an authorized SSH key, so turning off password authentication would leave no way to log in", ErrLockout)
	}
	if effective("permitrootlogin") == "no" && onlyRootIsKeyed(current.KeyedAccounts) && password == "no" {
		return fmt.Errorf("%w: root is the only account with an authorized key, and passwords are off", ErrLockout)
	}
	if err := guardAllowUsers(current, effective("allowusers"), password == "no"); err != nil {
		return err
	}
	return nil
}

// guardSSHPort refuses a port move the firewall would not admit.
//
// Only the certain case, like every other guard here: a firewall that is off,
// or one whose inbound default is allow, admits the new port already and the
// move is safe. An active default-deny firewall with no rule for the port is a
// lockout with a reload attached to it.
func guardSSHPort(port string, fw *FirewallStatus) error {
	if fw == nil || !fw.Available || !fw.Enabled {
		return nil
	}
	if fw.Policy.Incoming == "" || fw.Policy.Incoming == "allow" {
		return nil
	}
	for _, r := range fw.Rules {
		if r.Action != "ALLOW" && r.Action != "LIMIT" {
			continue
		}
		if r.Port == port || strings.HasPrefix(r.To, port+"/") || r.To == port {
			return nil
		}
		// A comma-separated rule covers several ports at once.
		for _, p := range strings.Split(r.Port, ",") {
			if strings.TrimSpace(p) == port {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: the firewall denies inbound traffic by default and no rule allows port %s. Add that rule first, then move sshd onto it",
		ErrLockout, port)
}

// guardAllowUsers refuses an allow list that would exclude everybody who could
// actually get in.
func guardAllowUsers(current *SSHDConfig, allow string, passwordsOff bool) error {
	allow = strings.TrimSpace(allow)
	if allow == "" || !passwordsOff {
		return nil
	}
	permitted := map[string]bool{}
	for _, name := range strings.Fields(allow) {
		permitted[name] = true
	}
	for _, account := range current.KeyedAccounts {
		if permitted[account.User] {
			return nil
		}
	}
	return fmt.Errorf("%w: none of the accounts holding an SSH key are on that list, and passwords are off",
		ErrLockout)
}

func onlyRootIsKeyed(accounts []KeyedAccount) bool {
	if len(accounts) != 1 {
		return false
	}
	return accounts[0].User == "root"
}

// applyDirectives rewrites configuration text so each directive is set once.
//
// The first value wins in sshd, so a directive already present is replaced
// where it stands rather than appended — appending would write a line the
// daemon reads and discards, which looks identical to a setting that took
// effect. Later duplicates are commented out with a marker rather than
// deleted, so an operator reading the file afterwards can see what was there.
func applyDirectives(content string, changes map[string]string, header bool) string {
	seen := map[string]bool{}
	var out []string
	if content != "" {
		sc := bufio.NewScanner(strings.NewReader(content))
		sc.Buffer(make([]byte, 0, 8192), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				out = append(out, line)
				continue
			}
			key, _, _ := strings.Cut(trimmed, " ")
			key = strings.ToLower(key)
			value, managed := changes[key]
			if !managed {
				out = append(out, line)
				continue
			}
			if seen[key] {
				out = append(out, "# "+line+"  # superseded, set above by Just Dashboard")
				continue
			}
			seen[key] = true
			if value == "" {
				out = append(out, "# "+line+"  # removed by Just Dashboard")
				continue
			}
			out = append(out, canonicalDirective(key)+" "+value)
		}
	}
	missing := []string{}
	for key, value := range changes {
		// An empty value means "no restriction", and there was nothing there
		// to comment out — writing the keyword with no argument would stop
		// sshd from starting.
		if !seen[key] && value != "" {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		if header && content == "" {
			out = append(out,
				"# Written by Just Dashboard. Included from sshd_config, which reads the",
				"# first value it finds for a keyword — so these override the settings",
				"# further down in sshd_config rather than being overridden by them.")
		} else {
			out = append(out, "# Set by Just Dashboard")
		}
		for _, key := range missing {
			out = append(out, canonicalDirective(key)+" "+changes[key])
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// canonicalDirective restores sshd's own capitalisation. sshd does not care,
// and a file where half the lines are lowercase reads as damaged.
func canonicalDirective(key string) string {
	for _, name := range []string{
		"PermitRootLogin", "PasswordAuthentication", "PubkeyAuthentication",
		"PermitEmptyPasswords", "X11Forwarding", "MaxAuthTries", "LoginGraceTime",
		"ClientAliveInterval", "ClientAliveCountMax", "Port", "AllowUsers", "DenyUsers",
		"AllowGroups", "DenyGroups", "AllowTcpForwarding", "GatewayPorts",
		"AllowAgentForwarding", "PermitTunnel", "MaxSessions",
	} {
		if strings.EqualFold(name, key) {
			return name
		}
	}
	return key
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func readFileOrEmpty(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// writeSSHFile writes through a temporary file in the same directory, keeping
// the previous content as .bak.
//
// An existing file keeps the mode it had: sshd_config is 0644 on most
// distributions and quietly tightening a system file to 0600 is a change
// nobody asked for. A file being created is 0600, which is the safe default
// for something sshd refuses to start behind if it is group-writable.
func writeSSHFile(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if prev, err := os.ReadFile(path); err == nil {
		os.WriteFile(path+".bak", prev, mode)
	}
	tmp, err := os.CreateTemp(dir, ".jd-sshd-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func restoreSSHFile(path, original string, existed bool) {
	if existed {
		mode := os.FileMode(0o600)
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
		os.WriteFile(path, []byte(original), mode)
		return
	}
	os.Remove(path)
}

func sshdTest(ctx context.Context) (bool, string) {
	bin, ok := sshdBinary()
	if !ok {
		return false, "sshd binary not found, so the configuration could not be tested"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := hostexec.CommandOnHost(ctx, bin, "-t").CombinedOutput()
	return err == nil, strings.TrimSpace(string(out))
}

// reloadSSH asks the daemon to re-read its configuration. The unit is called
// ssh on Debian-family systems and sshd on the others, and there is no way to
// know which without trying — so both are tried, and the socket-activated
// arrangement on recent Ubuntu is why ssh.socket is in the list too.
func reloadSSH(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var last string
	try := func(name string, args ...string) bool {
		if !hostexec.AvailableOnHost(name) {
			return false
		}
		out, err := hostexec.CommandOnHost(ctx, name, args...).CombinedOutput()
		if err == nil {
			return true
		}
		if text := strings.TrimSpace(string(out)); text != "" {
			last = text
		}
		return false
	}
	for _, unit := range []string{"ssh", "sshd", "ssh.socket"} {
		if try("systemctl", "reload-or-restart", unit) {
			return nil
		}
	}
	// Not every server runs systemd. Alpine and Devuan are the ones this
	// dashboard is most likely to meet — an OpenRC or SysV host would
	// otherwise be told its configuration was written and could not be picked
	// up, which is true only because nothing looked for the right command.
	for _, unit := range []string{"sshd", "ssh"} {
		if try("rc-service", unit, "reload") {
			return nil
		}
		if try("service", unit, "reload") {
			return nil
		}
	}
	if last == "" {
		last = "no ssh service responded to a reload"
	}
	return fmt.Errorf("%s", last)
}
