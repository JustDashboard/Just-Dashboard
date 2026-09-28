package proxysvc

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// The site form has a "password file" field, and nothing to put in it.
//
// That is the gap that makes basic auth useless from the dashboard: pointing
// at /etc/nginx/.htpasswd only helps somebody who has already been to a
// terminal and run htpasswd, which is the thing this page exists to avoid.
// Putting a staging site behind a password is one of the two or three most
// common reasons to reach for a reverse proxy at all.
//
// bcrypt rather than shelling to htpasswd: the tool is in apache2-utils, which
// is not installed on a host running nginx, and nginx has understood bcrypt
// hashes for over a decade. Doing it in-process also means the password never
// becomes an argv, which /proc/*/cmdline would make world-readable.

// Where these files live is Service.authDir(): beside the proxy config they
// belong to, and under the configured nginx directory rather than a hard-coded
// /etc/nginx — JD_NGINX_DIR exists for the hosts whose nginx is somewhere
// else, and a password file written where that nginx never looks is a site
// that refuses every visitor.

var (
	authFileRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	// A username may not contain a colon: the file format is user:hash, and a
	// colon in the first field silently truncates it.
	authUserRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)
	// bcryptHashRe is the one hash shape both proxies read: nginx through
	// libxcrypt, Caddy natively.
	bcryptHashRe = regexp.MustCompile(`^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$`)
)

// AuthFile is one password file and who is in it.
type AuthFile struct {
	Name  string   `json:"name"`
	Path  string   `json:"path"`
	Users []string `json:"users"`
	// UsedBy are the nginx sites whose blocks name this file, enabled or
	// not: a disabled site still refuses every login once it is enabled
	// again without its file.
	UsedBy []string `json:"usedBy"`
}

// ListAuthFiles enumerates the password files this dashboard manages.
func (s *Service) ListAuthFiles() []AuthFile {
	out := []AuthFile{}
	dir := s.authDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	used := s.authFileUsers()
	for _, e := range entries {
		if e.IsDir() || !authFileRe.MatchString(e.Name()) {
			continue
		}
		users, err := readAuthUsers(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		path := filepath.Join(dir, e.Name())
		out = append(out, AuthFile{
			Name: e.Name(), Path: path, Users: users, UsedBy: usedBy(used, path),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AuthFileUsedBy is the sites that name one password file.
func (s *Service) AuthFileUsedBy(file string) []string {
	return usedBy(s.authFileUsers(), filepath.Join(s.authDir(), file))
}

func usedBy(used map[string][]string, path string) []string {
	if sites := used[path]; sites != nil {
		return sites
	}
	return []string{}
}

// authFileUsers maps every password file an nginx site names to the sites
// naming it. It reads the site files themselves, and the snippets they
// include, rather than anything the dashboard remembers: a file wired in by
// hand, or by a deployment's route, is as much in use as one chosen in the
// site form, and the delete guard is only worth having if it sees all three.
// Caddy is not read: its basic_auth carries the hashes inline and names no
// file.
func (s *Service) authFileUsers() map[string][]string {
	used := map[string][]string{}
	seen := map[string]bool{}
	for _, sub := range []string{"sites-available", "sites-enabled", "conf.d"} {
		dir := filepath.Join(s.nginxDir, sub)
		for _, name := range siteFiles(dir) {
			full := filepath.Join(dir, name)
			real := resolvedFile(full)
			// A link in sites-enabled is the site sites-available already
			// counted, not a second one.
			if seen[real] {
				continue
			}
			seen[real] = true
			b, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			directives, err := ParseNginxFile(full, string(b), []string{"http"})
			if err != nil {
				continue
			}
			walk := siteWalk{s: s, path: full, self: real, features: map[string]bool{}}
			for _, d := range directives {
				if d.Name == "server" && d.Block != nil {
					walk.block(walk.expand(d.Block), true)
				}
			}
			for _, file := range walk.authFiles {
				used[file] = appendNew(used[file], name)
			}
		}
	}
	for _, sites := range used {
		sort.Strings(sites)
	}
	return used
}

// confPath is a path from a directive as nginx resolves it: a relative one
// is taken from the directory nginx.conf is in. A path built from variables
// cannot be resolved here and comes back empty.
func (s *Service) confPath(value string) string {
	if value == "" || strings.Contains(value, "$") {
		return ""
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(s.nginxDir, value)
	}
	return filepath.Clean(value)
}

func readAuthUsers(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	users := []string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if user, _, ok := strings.Cut(line, ":"); ok && user != "" {
			users = append(users, user)
		}
	}
	sort.Strings(users)
	return users, sc.Err()
}

// SetAuthUser adds or replaces one entry, creating the file if needed.
func (s *Service) SetAuthUser(file, user, password string) (*AuthFile, error) {
	if !authFileRe.MatchString(file) {
		return nil, fmt.Errorf("file name must be lowercase letters, digits, dots, dashes or underscores")
	}
	if !authUserRe.MatchString(user) {
		return nil, fmt.Errorf("%q is not a valid user name", user)
	}
	// bcrypt silently truncates at 72 bytes, so a longer password would
	// quietly become a shorter one — worth refusing rather than accepting a
	// password that is not the one somebody typed.
	if len(password) < 8 {
		return nil, fmt.Errorf("the password must be at least 8 characters")
	}
	if len(password) > 72 {
		return nil, fmt.Errorf("the password must be at most 72 characters, which is bcrypt's limit")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(s.authDir(), file)
	lines, err := readAuthLines(path)
	if err != nil {
		return nil, err
	}
	entry := user + ":" + string(hash)
	replaced := false
	for i, line := range lines {
		if existing, _, ok := strings.Cut(line, ":"); ok && existing == user {
			lines[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, entry)
	}
	if err := s.writeAuthFile(path, lines); err != nil {
		return nil, err
	}
	users, _ := readAuthUsers(path)
	return &AuthFile{Name: file, Path: path, Users: users, UsedBy: s.AuthFileUsedBy(file)}, nil
}

// RemoveAuthUser deletes one entry. Removing the last one leaves an empty
// file rather than deleting it. Neither stops nginx — `nginx -t` does not open
// the file, and passes with it missing — but a site whose file has vanished
// answers every login with a 403 and an error line per request, which reads
// as a server fault, while an empty file asks for credentials again like any
// wrong password (both checked on nginx 1.26.3).
func (s *Service) RemoveAuthUser(file, user string) (*AuthFile, error) {
	if !authFileRe.MatchString(file) {
		return nil, fmt.Errorf("invalid file name")
	}
	path := filepath.Join(s.authDir(), file)
	lines, err := readAuthLines(path)
	if err != nil {
		return nil, err
	}
	kept := lines[:0]
	found := false
	for _, line := range lines {
		if existing, _, ok := strings.Cut(line, ":"); ok && existing == user {
			found = true
			continue
		}
		kept = append(kept, line)
	}
	if !found {
		return nil, fmt.Errorf("%q is not in %s", user, file)
	}
	if err := s.writeAuthFile(path, kept); err != nil {
		return nil, err
	}
	users, _ := readAuthUsers(path)
	return &AuthFile{Name: file, Path: path, Users: users, UsedBy: s.AuthFileUsedBy(file)}, nil
}

// DeleteAuthFile removes a password file entirely. nginx does not notice at
// its next test or reload: a site still pointing at the file keeps running
// and refuses every login. The API refuses a file in use unless told to go
// ahead, through AuthFileUsedBy.
func (s *Service) DeleteAuthFile(file string) error {
	if !authFileRe.MatchString(file) {
		return fmt.Errorf("invalid file name")
	}
	if err := s.RefuseListedAuthFile(file); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.authDir(), file)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no such password file: %s", file)
		}
		return err
	}
	return nil
}

func readAuthLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	lines := []string{}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines, nil
}

// writeAuthFile writes through a temporary file in the same directory.
//
// The mode is the part that was wrong and produced a feature that could not
// work anywhere. nginx opens auth_basic_user_file in a *worker*, which runs as
// www-data, nginx or http depending on the distribution — not as the root that
// started the master and not as the root that writes this file. A 0640 file
// owned by root:root therefore gave "Permission denied" in the error log and a
// 403 to every visitor, which reads exactly like a wrong password.
//
// So the group is handed to whoever nginx's workers run as and the mode stays
// 0640. Where that user cannot be worked out, the file is 0644 instead: a
// world-readable list of bcrypt hashes is the same thing htpasswd itself
// produces by default, and it is a great deal better than a login nobody can
// pass.
func (s *Service) writeAuthFile(path string, lines []string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".jd-auth-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if gid, ok := s.nginxWorkerGID(); ok && os.Chown(tmp.Name(), -1, gid) == nil {
		mode = 0o640
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// nginxUserRe reads the `user` directive, which names the account nginx drops
// its workers to and optionally the group as well.
var nginxUserRe = regexp.MustCompile(`(?m)^\s*user\s+([^;#]+);`)

// nginxWorkerGID answers "who has to be able to read this".
//
// nginx.conf first, because that is the machine's own answer and the only one
// that is right on a host where somebody changed it. The three names after it
// are the defaults of every distribution family this runs on — www-data on
// Debian and Ubuntu, nginx on the RPM world and Alpine, http on Arch — tried
// in turn so a config that leaves the directive commented out still works.
func (s *Service) nginxWorkerGID() (int, bool) {
	names := []string{}
	if b, err := os.ReadFile(filepath.Join(s.nginxDir, "nginx.conf")); err == nil {
		if m := nginxUserRe.FindSubmatch(b); m != nil {
			fields := strings.Fields(string(m[1]))
			if len(fields) > 1 {
				// An explicit group wins: `user www-data adm;` means adm.
				if g, err := user.LookupGroup(fields[1]); err == nil {
					if gid, err := strconv.Atoi(g.Gid); err == nil {
						return gid, true
					}
				}
			}
			if len(fields) > 0 {
				names = append(names, fields[0])
			}
		}
	}
	names = append(names, "www-data", "nginx", "http")
	for _, name := range names {
		u, err := user.Lookup(name)
		if err != nil {
			continue
		}
		if gid, err := strconv.Atoi(u.Gid); err == nil {
			return gid, true
		}
	}
	return 0, false
}
