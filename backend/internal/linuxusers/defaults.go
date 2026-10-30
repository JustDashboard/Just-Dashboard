package linuxusers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The rules a new account is born under are scattered across three files with
// three formats, and a fourth directory whose contents every new home is copied
// from. This reads and writes only the keys below; every other line in those
// files, and every comment, stays as it was.

// AccountDefaults is the host's rules for accounts it has not made yet.
type AccountDefaults struct {
	// From /etc/login.defs. Zero with Known false means the file does not set it.
	PasswordMaxDays  int `json:"passwordMaxDays"`
	PasswordMinDays  int `json:"passwordMinDays"`
	PasswordWarnDays int `json:"passwordWarnDays"`
	// From /etc/default/useradd.
	Shell        string `json:"shell"`
	InactiveDays int    `json:"inactiveDays"`
	// From /etc/security/pwquality.conf, which only a host with pam_pwquality
	// reads at all.
	Quality PasswordQuality `json:"quality"`
	// Skel is what every new home starts with.
	Skel   []SkelFile `json:"skel"`
	Shells []string   `json:"shells"`
}

// PasswordQuality is pam_pwquality's two rules that decide most of it.
type PasswordQuality struct {
	Available  bool `json:"available"`
	MinLength  int  `json:"minLength"`
	MinClasses int  `json:"minClasses"`
}

// SkelFile is one entry of /etc/skel.
type SkelFile struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
	Dir   bool   `json:"dir"`
}

// DefaultsChange names what to change; a nil field is left alone.
type DefaultsChange struct {
	PasswordMaxDays  *int    `json:"passwordMaxDays,omitempty"`
	PasswordMinDays  *int    `json:"passwordMinDays,omitempty"`
	PasswordWarnDays *int    `json:"passwordWarnDays,omitempty"`
	Shell            *string `json:"shell,omitempty"`
	InactiveDays     *int    `json:"inactiveDays,omitempty"`
	MinLength        *int    `json:"minLength,omitempty"`
	MinClasses       *int    `json:"minClasses,omitempty"`
}

var (
	loginDefsLine = func(key string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^[ \t]*` + key + `[ \t]+(\S+)[ \t]*$`)
	}
	useraddLine = func(key string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^[ \t]*` + key + `=(.*)$`)
	}
	pwqualityLine = func(key string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^[ \t]*` + key + `[ \t]*=[ \t]*(\S+)[ \t]*$`)
	}
)

func readFileString(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func firstInt(re *regexp.Regexp, content string, fallback int) int {
	if m := re.FindStringSubmatch(content); m != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(m[1])); err == nil {
			return n
		}
	}
	return fallback
}

// Defaults reads all of it. A file that is not there reads as the value the tool
// would use without one.
func (s *Service) Defaults() *AccountDefaults {
	out := &AccountDefaults{
		PasswordMaxDays: -1, PasswordMinDays: 0, PasswordWarnDays: 7, InactiveDays: -1,
		Skel: []SkelFile{}, Shells: s.Shells(),
	}
	if login, ok := readFileString(s.path("login.defs")); ok {
		out.PasswordMaxDays = firstInt(loginDefsLine("PASS_MAX_DAYS"), login, -1)
		out.PasswordMinDays = firstInt(loginDefsLine("PASS_MIN_DAYS"), login, 0)
		out.PasswordWarnDays = firstInt(loginDefsLine("PASS_WARN_AGE"), login, 7)
	}
	if out.PasswordMaxDays >= 10000 {
		out.PasswordMaxDays = -1
	}
	if useradd, ok := readFileString(s.path(filepath.Join("default", "useradd"))); ok {
		if m := useraddLine("SHELL").FindStringSubmatch(useradd); m != nil {
			out.Shell = strings.TrimSpace(m[1])
		}
		out.InactiveDays = firstInt(useraddLine("INACTIVE"), useradd, -1)
	}
	if quality, ok := readFileString(s.path(filepath.Join("security", "pwquality.conf"))); ok {
		out.Quality.Available = true
		out.Quality.MinLength = firstInt(pwqualityLine("minlen"), quality, 8)
		out.Quality.MinClasses = firstInt(pwqualityLine("minclass"), quality, 0)
	}
	if entries, err := os.ReadDir(s.path("skel")); err == nil {
		for _, entry := range entries {
			file := SkelFile{Name: entry.Name(), Dir: entry.IsDir()}
			if info, err := entry.Info(); err == nil && !entry.IsDir() {
				file.Bytes = info.Size()
			}
			out.Skel = append(out.Skel, file)
		}
		sort.Slice(out.Skel, func(i, j int) bool { return out.Skel[i].Name < out.Skel[j].Name })
	}
	return out
}

// SetDefaults writes each change into its own file.
func (s *Service) SetDefaults(change DefaultsChange) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if change.Shell != nil {
		if err := s.validateShell(*change.Shell); err != nil {
			return err
		}
	}
	for _, days := range []*int{change.PasswordMaxDays, change.PasswordMinDays, change.PasswordWarnDays, change.InactiveDays} {
		if days != nil && (*days < -1 || *days > 99999) {
			return fmt.Errorf("a password rule is a number of days between -1 and 99999")
		}
	}
	if change.MinLength != nil && (*change.MinLength < 6 || *change.MinLength > 128) {
		return fmt.Errorf("a minimum password length is between 6 and 128")
	}
	if change.MinClasses != nil && (*change.MinClasses < 0 || *change.MinClasses > 4) {
		return fmt.Errorf("character classes are between 0 and 4")
	}
	maxDays := change.PasswordMaxDays
	if maxDays != nil && *maxDays < 0 {
		maxDays = intPtr(99999)
	}
	login := []keyValue{}
	addInt := func(list *[]keyValue, key string, value *int) {
		if value != nil {
			*list = append(*list, keyValue{key, strconv.Itoa(*value)})
		}
	}
	addInt(&login, "PASS_MAX_DAYS", maxDays)
	addInt(&login, "PASS_MIN_DAYS", change.PasswordMinDays)
	addInt(&login, "PASS_WARN_AGE", change.PasswordWarnDays)
	if err := s.editKeys(s.path("login.defs"), login, loginDefsLine, "%s\t%s"); err != nil {
		return err
	}
	useradd := []keyValue{}
	if change.Shell != nil {
		useradd = append(useradd, keyValue{"SHELL", *change.Shell})
	}
	addInt(&useradd, "INACTIVE", change.InactiveDays)
	if err := s.editKeys(s.path(filepath.Join("default", "useradd")), useradd, useraddLine, "%s=%s"); err != nil {
		return err
	}
	quality := []keyValue{}
	addInt(&quality, "minlen", change.MinLength)
	addInt(&quality, "minclass", change.MinClasses)
	if len(quality) > 0 {
		if _, err := os.Stat(s.path(filepath.Join("security", "pwquality.conf"))); err != nil {
			return fmt.Errorf("pam_pwquality is not installed on this host")
		}
	}
	return s.editKeys(s.path(filepath.Join("security", "pwquality.conf")), quality, pwqualityLine, "%s = %s")
}

type keyValue struct{ key, value string }

// editKeys replaces each key's live line, or adds one at the end. It writes a
// sibling and renames it, so a reader never sees half a login.defs, and keeps the
// file's mode.
func (s *Service) editKeys(path string, changes []keyValue, line func(string) *regexp.Regexp, format string) error {
	if len(changes) == 0 {
		return nil
	}
	content, _ := readFileString(path)
	for _, change := range changes {
		rendered := fmt.Sprintf(format, change.key, change.value)
		re := line(change.key)
		if re.MatchString(content) {
			// Only the first: login.defs reads the first, and a second copy is
			// someone else's mistake to keep or fix.
			done := false
			content = re.ReplaceAllStringFunc(content, func(match string) string {
				if done {
					return match
				}
				done = true
				return rendered
			})
			continue
		}
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += rendered + "\n"
	}
	return writeAtomic(path, []byte(content))
}

func writeAtomic(path string, content []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp := path + ".jd-" + hex.EncodeToString(token[:])
	if err := os.WriteFile(temp, content, mode); err != nil {
		return err
	}
	defer os.Remove(temp)
	if err := os.Chmod(temp, mode); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
